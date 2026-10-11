package generate

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/ar2/pkg/migrate"
)

// inferSigning records, per environment, how the release signs that asset.
//
// A release says how it is signed by what it carries: a .sigstore.json, a .sig and
// a .pem, a cosign.pub. aqua gr reads the checksum file's signature that way, and
// the same reading applies to each asset when the generated file holds one entry per
// environment.
//
// What aqua gr can't read off an asset list is who signed, so it constrains the
// identity to a workflow of the package's own repository and leaves it at that. It
// reads a list because it reads one for every release in a package's history. Here
// there is one release, and the bundle it names is a few kilobytes beside an asset that
// is downloaded whole a moment later, so the signer is read rather than assumed.
//
// A definition that already says how the asset is signed wins. It was written by
// someone who knows the release, and it can name a signer nothing in the release does
// -- gosec's public key lives in the repository rather than in the release.
//
// Only cosign is inferred. SLSA provenance already is, by aqua gr, from an asset
// ending in .intoto.jsonl. Minisign is not: a .minisig says a signature exists but
// not which public key it should verify against, and a signature checked against no
// particular key is not a check.
func (g *Generator) inferSigning(ctx context.Context, logger *slog.Logger, input *Input, reg *Registry, assetNames map[string]struct{}) error {
	owner, name, err := repo(input.PkgName)
	if err != nil {
		return err
	}
	for _, asset := range reg.Assets {
		// The definition may name the signing workflow by the spelling aqua has
		// deprecated. A file being generated now writes the one aqua reads first.
		asset.GitHubArtifactAttestations = migrate.Attestations(asset.GitHubArtifactAttestations)
		if asset.Asset == "" || asset.Cosign != nil {
			continue
		}
		cosign := genrgst.InferCosign(owner, name, asset.Asset, assetNames)
		if cosign == nil {
			continue
		}
		asset.Cosign = g.heldToTheSigner(ctx, logger, owner, name, input.Version, cosign)
	}
	g.inferPackslip(ctx, logger, owner, name, input.Version, reg, assetNames)
	return nil
}

// inferPackslip records, on each entry the release's packslip bundle covers, who signed the
// bundle.
//
// packslip publishes one signed bundle for the whole release, packslip.sigstore.json, which
// records the digests of its artifacts. aqua gr pins the signer to a workflow of the
// package's own repository, as an identity prefix. The bundle is a few kilobytes and says
// who signed it, so the signer is read rather than assumed: the identity, ref included, and
// the issuer that vouched for it. Every entry of the release shares the one bundle, so it is
// read once.
//
// A definition saying whom to trust wins, as it does for cosign. A bundle that can't be read
// leaves aqua gr's prefix, for the verification that follows to decide. A bundle signed with
// a key has no identity, and nothing in the release says which key, so an entry pinned to
// nothing would be no check: it gets no packslip unless the definition names the key.
func (g *Generator) inferPackslip(ctx context.Context, logger *slog.Logger, owner, name, version string, reg *Registry, assetNames map[string]struct{}) {
	if _, ok := assetNames[aquaregistry.BundleAssetPackslip]; !ok {
		return
	}
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		owner, name, pathEscape(version), aquaregistry.BundleAssetPackslip)
	signer, err := readSigner(ctx, g.httpClient, url)
	if err != nil {
		logger.Warn("failed to read who signed the packslip bundle", "bundle", url, "error", err.Error())
		signer = nil
	}
	for _, asset := range reg.Assets {
		if !inRelease(asset, owner, name) {
			continue
		}
		ps := asset.Packslip
		if ps == nil {
			ps = &aquaregistry.Packslip{}
		}
		if !enabled(ps.Enabled) {
			continue
		}
		asset.Packslip = heldToPackslip(logger, ps, signer, owner, name)
	}
}

// heldToPackslip is the entry's packslip configuration, held to the bundle's signer when the
// definition doesn't say whom to trust.
func heldToPackslip(logger *slog.Logger, ps *aquaregistry.Packslip, s *signer, owner, name string) *aquaregistry.Packslip {
	if ps.Identity != "" || ps.PublicKey != "" || ps.Pin != "" {
		return ps
	}
	switch {
	case s == nil:
		if ps.IdentityPrefix == "" {
			ps.IdentityPrefix = fmt.Sprintf("https://github.com/%s/%s/", owner, name)
		}
		if ps.Issuer == "" {
			ps.Issuer = issuerOrGitHub("")
		}
		return ps
	case s.Identity == "":
		if ps.IdentityPrefix != "" {
			return ps
		}
		logger.Warn("the release signs its packslip bundle with a key rather than with an identity, which only a definition can name")
		return nil
	}
	ps.Identity = s.Identity
	ps.IdentityPrefix = ""
	ps.Issuer = issuerOrGitHub(s.Issuer)
	return ps
}

// inRelease reports whether the entry downloads an asset of the package's own release on
// GitHub, which is the release whose packslip bundle covers it.
func inRelease(asset *Asset, owner, name string) bool {
	if asset.Type != aquaregistry.PkgInfoTypeGitHubRelease || asset.Host != "" || asset.Asset == "" {
		return false
	}
	return (asset.RepoOwner == "" || strings.EqualFold(asset.RepoOwner, owner)) &&
		(asset.RepoName == "" || strings.EqualFold(asset.RepoName, name))
}

// pathEscape escapes a tag for a release download URL's path.
func pathEscape(tag string) string {
	return url.PathEscape(tag)
}

// heldToTheSigner replaces what the inference assumed about the signer with what the
// release says, and drops the configuration when the release says the assumption can't
// hold.
//
// The inference constrains the identity to a workflow of the package's own repository,
// which is where most releases are signed and not where all of them are: smallstep signs
// its releases from a workflow in smallstep/workflows, so an entry naming
// smallstep/certificates is held to an identity the certificate doesn't carry and no
// version merges under it.
//
// What says who signed is a file beside the asset: a bundle, or the certificate that goes
// with a signature. Either is a few kilobytes next to an asset that is downloaded whole a
// moment later, so the signer is read rather than assumed.
//
// A bundle signed with a key has no identity in it. Holding it to one can't succeed, so an
// entry saying it does is an entry no version can merge under; what such a release needs is
// a definition naming the key, which nothing here can find.
func (g *Generator) heldToTheSigner(ctx context.Context, logger *slog.Logger, owner, name, version string, cosign *aquaregistry.Cosign) *aquaregistry.Cosign {
	url, read, ok := signerMaterial(owner, name, version, cosign)
	if !ok {
		return cosign
	}
	signer, err := read(ctx, g.httpClient, url)
	if err != nil {
		// Reading it is how the assumption is improved, not how it is made, so material
		// that can't be read leaves it to the verification that follows.
		logger.Warn("failed to read what says who signed the asset",
			"material", url, "error", err.Error())
		return cosign
	}
	if signer.Identity == "" {
		logger.Warn("the release signs the asset with a key rather than with an identity, which only a definition can name",
			"material", url)
		return nil
	}
	cosign.Opts = heldTo(cosign.Opts, signer)
	return cosign
}

// signerMaterial is where in the release it says who signed, and how to read it.
//
// The bundle first: a release that publishes one publishes everything in it, and a
// certificate beside it would say the same thing. A release with neither is one the
// inference had nothing to go on for, which is where this is of no help either.
//
// The bundle is named as an asset, the certificate as the argument cosign reads it from,
// so the certificate's URL arrives with the version still a template in it.
func signerMaterial(owner, name, version string, cosign *aquaregistry.Cosign) (string, reader, bool) {
	if cosign.Bundle != nil && cosign.Bundle.Asset != nil {
		return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
			owner, name, version, *cosign.Bundle.Asset), readSigner, true
	}
	if url, ok := optValue(cosign.Opts, flagCertificate); ok {
		return strings.ReplaceAll(url, "{{.Version}}", version), readCertificateSigner, true
	}
	return "", nil, false
}

// reader reads who signed out of one of the files the release publishes to say so.
type reader func(context.Context, *http.Client, string) (*signer, error)

// The arguments the inference writes for the identity, and the one naming the
// certificate to read it from.
const (
	flagCertificate        = "--certificate"
	flagCertIdentity       = "--certificate-identity"
	flagCertIdentityRegexp = "--certificate-identity-regexp"
	flagCertOIDCIssuer     = "--certificate-oidc-issuer"
)

// heldTo holds the arguments to the signer the release names, and leaves the rest of
// them as they are.
//
// Only the identity was assumed. The certificate and the signature are where cosign
// reads the signature from, and an entry that had those dropped for an identity would
// verify nothing at all. The regexp becomes the plain identity: one signer is known
// now, and a pattern for it would only be a pattern that could match another.
func heldTo(opts []string, s *signer) []string {
	held := make([]string, 0, len(opts)+2) //nolint:mnd // the two an entry can be missing
	identified, issued := false, false
	for len(opts) > 0 {
		flag := opts[0]
		opts = opts[1:]
		switch flag {
		case flagCertIdentity, flagCertIdentityRegexp:
			opts = skipValue(opts)
			held = append(held, flagCertIdentity, s.Identity)
			identified = true
		case flagCertOIDCIssuer:
			opts = skipValue(opts)
			held = append(held, flagCertOIDCIssuer, issuerOrGitHub(s.Issuer))
			issued = true
		default:
			held = append(held, flag)
		}
	}
	if !identified {
		held = append(held, flagCertIdentity, s.Identity)
	}
	if !issued {
		held = append(held, flagCertOIDCIssuer, issuerOrGitHub(s.Issuer))
	}
	return held
}

// skipValue drops the value of the flag just read, which a flag at the end doesn't have.
func skipValue(opts []string) []string {
	if len(opts) == 0 {
		return opts
	}
	return opts[1:]
}

// optValue is what the given flag was written with, if it was written.
func optValue(opts []string, flag string) (string, bool) {
	for i, opt := range opts {
		if opt == flag && i+1 < len(opts) {
			return opts[i+1], true
		}
	}
	return "", false
}

// githubOIDCIssuer is who vouches for a workflow's identity, and what a certificate
// that doesn't record its issuer was signed for.
const githubOIDCIssuer = "https://token.actions.githubusercontent.com"

func issuerOrGitHub(issuer string) string {
	if issuer == "" {
		return githubOIDCIssuer
	}
	return issuer
}
