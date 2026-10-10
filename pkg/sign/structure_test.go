package sign_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/google/go-cmp/cmp"
)

// What cosign's command line says moves into the fields that say it, and what no field
// names stays on the command line.
func TestStructure(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{
		Type:      "github_release",
		RepoOwner: "sigstore",
		RepoName:  "cosign",
		Cosign: &aquaregistry.Cosign{Opts: []string{
			"--signature", "https://github.com/sigstore/cosign/releases/download/v2.4.1/cosign-linux-amd64.sig",
			"--certificate=https://example.com/cosign.pem",
			"--certificate-identity", "keyless@projectsigstore.iam.gserviceaccount.com",
			"--certificate-oidc-issuer", "https://accounts.google.com",
			"--insecure-ignore-tlog",
			"--key", "/etc/key.pub",
		}},
	}
	sign.Structure(asset, "v2.4.1")
	want := &aquaregistry.Cosign{
		Opts: []string{"--insecure-ignore-tlog", "--key", "/etc/key.pub"},
		Signature: &aquaregistry.DownloadedFile{
			Type: "github_release", RepoOwner: "sigstore", RepoName: "cosign",
			Asset: new("cosign-linux-amd64.sig"),
		},
		Certificate:           &aquaregistry.DownloadedFile{Type: "http", URL: new("https://example.com/cosign.pem")},
		CertificateIdentity:   "keyless@projectsigstore.iam.gserviceaccount.com",
		CertificateOIDCIssuer: "https://accounts.google.com",
	}
	if diff := cmp.Diff(want, asset.Cosign); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// A release asset of another version or another repository is a URL: aqua downloads a
// github_release file from the package's own repository at the version being installed.
func TestStructure_otherRelease(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{
		Type: "github_release", RepoOwner: "foo", RepoName: "bar",
		Cosign: &aquaregistry.Cosign{Opts: []string{
			"--signature", "https://github.com/foo/bar/releases/download/v1.0.0/x.sig",
			"--certificate", "https://github.com/other/repo/releases/download/v2.0.0/x.pem",
		}},
	}
	sign.Structure(asset, "v2.0.0")
	if got := asset.Cosign.Signature; got.Type != "http" || *got.URL != "https://github.com/foo/bar/releases/download/v1.0.0/x.sig" {
		t.Errorf("signature: %+v", got)
	}
	if got := asset.Cosign.Certificate; got.Type != "http" {
		t.Errorf("certificate: %+v", got)
	}
	if asset.Cosign.Opts != nil {
		t.Errorf("opts left: %v", asset.Cosign.Opts)
	}
}

// A flag whose field is already set stays a flag rather than overwriting it, so nothing
// the entry said is lost.
func TestStructure_alreadySet(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{Cosign: &aquaregistry.Cosign{
		Bundle: &aquaregistry.DownloadedFile{Type: "github_release", Asset: new("x.sigstore.json")},
		Opts:   []string{"--bundle", "https://example.com/y.sigstore.json"},
	}}
	sign.Structure(asset, "v1.0.0")
	if diff := cmp.Diff([]string{"--bundle", "https://example.com/y.sigstore.json"}, asset.Cosign.Opts); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
