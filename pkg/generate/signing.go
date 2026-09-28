package generate

import (
	"context"
	"fmt"
	"log/slog"

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
	return nil
}

// heldToTheSigner replaces what the inference assumed about the signer with what the
// bundle says, and drops the configuration when the bundle says the assumption can't
// hold.
//
// A bundle signed with a key has no identity in it. Holding it to one can't succeed, so
// an entry saying it does is an entry no version can merge under; what such a release
// needs is a definition naming the key, which nothing here can find.
func (g *Generator) heldToTheSigner(ctx context.Context, logger *slog.Logger, owner, name, version string, cosign *aquaregistry.Cosign) *aquaregistry.Cosign {
	if cosign.Bundle == nil || cosign.Bundle.Asset == nil {
		return cosign
	}
	bundle := *cosign.Bundle.Asset
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, name, version, bundle)
	signer, err := readSigner(ctx, g.httpClient, url)
	if err != nil {
		// Reading the bundle is how the assumption is improved, not how it is made, so
		// a bundle that can't be read leaves it to the verification that follows.
		logger.Warn("failed to read the signature bundle", "bundle", bundle, "error", err.Error())
		return cosign
	}
	if signer.Identity == "" {
		logger.Warn("the release signs the asset with a key rather than with an identity, which only a definition can name",
			"bundle", bundle)
		return nil
	}
	cosign.Opts = []string{
		"--certificate-identity", signer.Identity,
		"--certificate-oidc-issuer", issuerOrGitHub(signer.Issuer),
	}
	return cosign
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
