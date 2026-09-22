package sign

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

// flagIdentity and flagIdentityRegexp are how cosign is told who to accept.
//
// aqua gr writes the pattern, because a release's asset names say that something is
// signed and not by whom. The signature itself says, so the pattern is replaced by
// the name once it has been read.
const (
	flagIdentity       = "--certificate-identity"
	flagIdentityRegexp = "--certificate-identity-regexp"
	flagIssuer         = "--certificate-oidc-issuer"
)

// pin records who signed the asset, replacing a guessed pattern with the name read
// off the signature. It returns the identity the entry ends up asking for.
//
// A definition that already names the signer is left alone. That is the pinned value
// a maintainer reviewed, and a release signed by anyone else has to fail against it
// rather than quietly redefine what the package is signed by.
func (v *Verifier) pin(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset) *Identity {
	if !asset.Cosign.GetEnabled() {
		return nil
	}
	observed, err := v.observe(ctx, version, asset)
	if err != nil {
		// The signature is still verified; only the chance to replace a guess with
		// a name is lost. What it was guessed to be may well be right.
		logger.Debug("couldn't read who signed the asset",
			"os", asset.OS, "arch", asset.Arch, "error", err.Error())
		return nil
	}

	if pinned := identityOpt(asset.Cosign.Opts, flagIdentity); pinned != "" {
		if rendered := strings.ReplaceAll(pinned, "{{.Version}}", version); rendered != observed.SAN {
			logger.Warn("this version was signed by someone else than the definition names",
				"os", asset.OS, "arch", asset.Arch,
				"definition", pinned, "signed_by", observed.SAN)
		}
		return nil
	}

	logger.Debug("read who signed the asset",
		"os", asset.OS, "arch", asset.Arch, "identity", observed.SAN, "issuer", observed.Issuer)
	asset.Cosign.Opts = replaceIdentity(asset.Cosign.Opts, observed)
	return observed
}

// TemplateVersion puts the version back where a value holds it.
//
// A workflow signs under the ref it ran for, so its name holds the version, and a
// definition pinning it as it stands would only ever match the one release. A
// service account signs under a name that doesn't move and is pinned as it is.
//
// It is the definition that wants this. What a run generates describes one version
// and holds no templates at all.
func TemplateVersion(s, version string) string {
	if version == "" || !strings.Contains(s, version) {
		return s
	}
	return strings.Replace(s, version, "{{.Version}}", 1)
}

// identityOpt returns the value of a cosign flag, or an empty string when it isn't
// there.
func identityOpt(opts []string, flag string) string {
	for i, opt := range opts {
		if opt == flag && i+1 < len(opts) {
			return opts[i+1]
		}
	}
	return ""
}

// replaceIdentity swaps the guessed pattern for the name that was read, leaving
// everything else cosign was going to be told.
func replaceIdentity(opts []string, id *Identity) []string {
	out := make([]string, 0, len(opts)+2) //nolint:mnd // the identity is a flag and its value
	for i := 0; i < len(opts); i++ {
		if (opts[i] == flagIdentityRegexp || opts[i] == flagIssuer) && i+1 < len(opts) {
			i++
			continue
		}
		out = append(out, opts[i])
	}
	return append(out, id.Opts()...)
}

// observe downloads whatever the entry points at as its signature and reads the
// signer out of it.
func (v *Verifier) observe(ctx context.Context, version string, asset *generate.Asset) (*Identity, error) {
	if bundle := asset.Cosign.Bundle; bundle != nil {
		b, err := v.fetch(ctx, version, asset, bundle)
		if err != nil {
			return nil, err
		}
		return identityFromBundle(b)
	}
	if cert := asset.Cosign.Certificate; cert != nil {
		b, err := v.fetch(ctx, version, asset, cert)
		if err != nil {
			return nil, err
		}
		return identityFromPEM(b)
	}
	// A certificate given as a URL among the options rather than as a file of its
	// own, which is the shape aqua gr writes when a release has no bundle.
	if url := identityOpt(asset.Cosign.Opts, "--certificate"); url != "" {
		b, err := v.get(ctx, render(url, version, asset))
		if err != nil {
			return nil, err
		}
		return identityFromPEM(b)
	}
	return nil, errNoCertificate
}

// fetch downloads a file the signing configuration refers to.
func (v *Verifier) fetch(ctx context.Context, version string, asset *generate.Asset, file *aquaregistry.DownloadedFile) ([]byte, error) {
	name := file.Asset
	if name == nil || *name == "" {
		if file.URL == nil || *file.URL == "" {
			return nil, errNoCertificate
		}
		return v.get(ctx, render(*file.URL, version, asset))
	}
	return v.get(ctx, fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		asset.RepoOwner, asset.RepoName, version, render(*name, version, asset)))
}

// render fills in the values a signing configuration is written with. The asset is
// already resolved, so the environment needs no templating of its own.
func render(s, version string, asset *generate.Asset) string {
	r := strings.NewReplacer(
		"{{.Version}}", version,
		"{{trimV .Version}}", strings.TrimPrefix(version, "v"),
		"{{.Asset}}", asset.Asset,
		"{{.OS}}", asset.OS,
		"{{.Arch}}", asset.Arch,
	)
	return r.Replace(s)
}

func (v *Verifier) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create a request for the signature: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download the signature: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the signature: status code %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body) //nolint:wrapcheck // the caller says what it was reading
}

// Render fills in the templates an entry's signing configuration still holds.
//
// The definition writes them, and the file being generated describes one version of
// one environment, so nothing in it should have to be worked out again later. It is
// done last because the pattern a signer was guessed by, and the name it was
// replaced with, are both written with the version in them.
func Render(asset *generate.Asset, version string) {
	if asset.Cosign != nil {
		for i, opt := range asset.Cosign.Opts {
			asset.Cosign.Opts[i] = render(opt, version, asset)
		}
		for _, f := range []*aquaregistry.DownloadedFile{
			asset.Cosign.Signature, asset.Cosign.Certificate, asset.Cosign.Key, asset.Cosign.Bundle,
		} {
			renderFile(f, version, asset)
		}
	}
	if asset.SLSAProvenance != nil {
		asset.SLSAProvenance.Asset = renderPtr(asset.SLSAProvenance.Asset, version, asset)
		asset.SLSAProvenance.URL = renderPtr(asset.SLSAProvenance.URL, version, asset)
	}
	if asset.Minisign != nil {
		asset.Minisign.Asset = renderPtr(asset.Minisign.Asset, version, asset)
		asset.Minisign.URL = renderPtr(asset.Minisign.URL, version, asset)
	}
}

func renderFile(f *aquaregistry.DownloadedFile, version string, asset *generate.Asset) {
	if f == nil {
		return
	}
	f.Asset = renderPtr(f.Asset, version, asset)
	f.URL = renderPtr(f.URL, version, asset)
}

func renderPtr(s *string, version string, asset *generate.Asset) *string {
	if s == nil {
		return nil
	}
	rendered := render(*s, version, asset)
	return &rendered
}
