package generate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

// Every entry of every version starts with the definition's signing objects. What is
// recorded on one of them -- the commit one version was built from, cosign's command line
// rendered for one asset -- must not reach the others or the definition: v2.98.0 of cli/cli
// was held to v2.99.0's commit, and its attestation didn't verify.
func TestOwnSigning(t *testing.T) {
	t.Parallel()
	def := &aquaregistry.PackageInfo{
		GitHubArtifactAttestations: &aquaregistry.GitHubArtifactAttestations{SignerWorkflow2: "cli/cli/.github/workflows/deployment.yml"},
		Cosign: &aquaregistry.Cosign{
			Opts:   []string{"--signature", "https://example.com/{{.Version}}.sig"},
			Bundle: &aquaregistry.DownloadedFile{Type: "github_release", Asset: new("{{.Version}}.sigstore.json")},
		},
	}
	versions := []*generate.Registry{{}, {}}
	for _, reg := range versions {
		reg.Assets = []*generate.Asset{
			{GitHubArtifactAttestations: def.GitHubArtifactAttestations, Cosign: def.Cosign},
			{GitHubArtifactAttestations: def.GitHubArtifactAttestations, Cosign: def.Cosign},
		}
		generate.OwnSigning(reg)
	}

	first := versions[0].Assets[0]
	first.GitHubArtifactAttestations.SourceDigest = "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0"
	first.Cosign.Opts[1] = "https://example.com/v2.99.0.sig"
	*first.Cosign.Bundle.Asset = "v2.99.0.sigstore.json"

	for i, reg := range versions {
		for j, asset := range reg.Assets {
			if i == 0 && j == 0 {
				continue
			}
			checkUntouched(t, i, j, asset)
		}
	}
	if def.GitHubArtifactAttestations.SourceDigest != "" || def.Cosign.Opts[1] != "https://example.com/{{.Version}}.sig" {
		t.Error("the definition was written to")
	}
}

func checkUntouched(t *testing.T, i, j int, asset *generate.Asset) {
	t.Helper()
	if asset.GitHubArtifactAttestations.SourceDigest != "" {
		t.Errorf("version %d entry %d has the first entry's commit", i, j)
	}
	if asset.Cosign.Opts[1] != "https://example.com/{{.Version}}.sig" || *asset.Cosign.Bundle.Asset != "{{.Version}}.sigstore.json" {
		t.Errorf("version %d entry %d has the first entry's rendering", i, j)
	}
}
