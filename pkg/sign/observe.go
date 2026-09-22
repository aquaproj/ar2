package sign

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/szksh-lab-2/ar2/pkg/generate"
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

	templated := observed.template(version)
	logger.Debug("read who signed the asset",
		"os", asset.OS, "arch", asset.Arch, "identity", templated.SAN, "issuer", templated.Issuer)
	asset.Cosign.Opts = replaceIdentity(asset.Cosign.Opts, templated)
	return templated
}

// template puts the version back where the signer's name holds it.
//
// A workflow signs under the ref it ran for, so the name holds the version and
// pinning it as it stands would only ever match the one release. A service account
// signs under a name that doesn't move, and is pinned as it is.
func (i *Identity) template(version string) *Identity {
	if version == "" || !strings.Contains(i.SAN, version) {
		return i
	}
	return &Identity{
		SAN:    strings.Replace(i.SAN, version, "{{.Version}}", 1),
		Issuer: i.Issuer,
	}
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
