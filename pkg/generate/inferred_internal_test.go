package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// A kind of signing no entry carried before the release was read is inferred; one the
// definition wrote for some entry isn't, whichever entries it was added to after.
func TestInferredKinds(t *testing.T) {
	t.Parallel()
	asset := "multiple.intoto.jsonl"
	reg := &Registry{Assets: []*Asset{
		{OS: "linux", Arch: "amd64", Cosign: &aquaregistry.Cosign{}},
		{OS: "darwin", Arch: "arm64"},
	}}
	declared := signingKinds(reg)
	reg.Assets[1].Cosign = &aquaregistry.Cosign{}
	reg.Assets[1].SLSAProvenance = &aquaregistry.SLSAProvenance{Type: "github_release", Asset: &asset}
	if diff := cmp.Diff(map[string]bool{KindSLSAProvenance: true}, inferredKinds(declared, reg)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	DropSigning(reg, KindSLSAProvenance)
	if reg.Assets[1].SLSAProvenance != nil || reg.Assets[1].Cosign == nil {
		t.Error("dropping the provenance took off something else, or not it")
	}
}
