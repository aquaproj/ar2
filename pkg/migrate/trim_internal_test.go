package migrate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// aqua-registry still holds entries written as signer-workflow, which aqua accepts
// and has deprecated. A registry being written now carries the name aqua reads
// first.
func TestAttestations(t *testing.T) {
	t.Parallel()
	got := Attestations(&aquaregistry.GitHubArtifactAttestations{
		SignerWorkflow3: "taiki-e/github-actions/.github/workflows/rust-release.yml", //nolint:staticcheck // the deprecated name is what is being translated
	})
	if got.SignerWorkflow2 != "taiki-e/github-actions/.github/workflows/rust-release.yml" {
		t.Errorf("signer_workflow is %q", got.SignerWorkflow2)
	}
	if got.SignerWorkflow3 != "" { //nolint:staticcheck // checking it was translated away
		t.Errorf("the deprecated name survived: %q", got.SignerWorkflow3) //nolint:staticcheck // as above
	}
}

// One already written the current way is left as it is, and nil stays nil.
func TestAttestations_current(t *testing.T) {
	t.Parallel()
	in := &aquaregistry.GitHubArtifactAttestations{SignerWorkflow2: "cli/cli/.github/workflows/deployment.yml"}
	if got := Attestations(in); got != in {
		t.Errorf("it was copied for no reason: %+v", got)
	}
	if got := Attestations(nil); got != nil {
		t.Errorf("nil became %+v", got)
	}
}
