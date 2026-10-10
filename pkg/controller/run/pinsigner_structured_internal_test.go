package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
)

// An entry generated since ar2 v0.12 holds cosign as fields. The definition is still told
// who signs, as the command line it has always held.
func TestPinSigner_structured(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{
		OS: "linux", Arch: "amd64", Type: "github_release", RepoOwner: "cli", RepoName: "cli",
		Cosign: &aquaregistry.Cosign{Opts: []string{
			"--certificate-identity", "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/tags/v2.0.0",
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
			"--signature", "https://github.com/cli/cli/releases/download/v2.0.0/gh.sig",
		}},
	}
	sign.Structure(asset, "v2.0.0")
	if asset.Cosign.CertificateIdentity == "" {
		t.Fatal("the entry wasn't structured")
	}
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	pinSigner(discardLogger(), "cli/cli", cfg, []*version{{Version: "v2.0.0", Registry: registryOf(asset)}})
	if cfg.Cosign == nil {
		t.Fatal("the definition wasn't told who signs")
	}
	want := "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/tags/{{.Version}}"
	if got := identityOf(cfg.Cosign.Opts); got != want {
		t.Errorf("the identity is %q, want %q", got, want)
	}
}

// The commit and the ref an attestation was built from are one version's, and stay out of
// the definition every version is generated from.
func TestPinSigner_attestationIsntPinnedToABuild(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{OS: "linux", Arch: "amd64", GitHubArtifactAttestations: &aquaregistry.GitHubArtifactAttestations{
		SignerWorkflow2:       "cli/cli/.github/workflows/deployment.yml",
		SourceRef:             "refs/heads/trunk",
		SourceDigest:          "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0",
		CertOIDCIssuer:        "https://token.actions.githubusercontent.com",
		DenySelfHostedRunners: true,
	}}
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	pinSigner(discardLogger(), "cli/cli", cfg, []*version{{Version: "v2.0.0", Registry: registryOf(asset)}})
	got := cfg.GitHubArtifactAttestations
	if got == nil || got.SignerWorkflow() != "cli/cli/.github/workflows/deployment.yml" {
		t.Fatalf("the workflow wasn't recorded: %+v", got)
	}
	if got.SourceDigest != "" || got.SourceRef != "" || got.CertOIDCIssuer != "" || got.DenySelfHostedRunners {
		t.Errorf("the definition holds one build's values: %+v", got)
	}
}
