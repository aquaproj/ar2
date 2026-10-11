package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/google/go-cmp/cmp"
)

// An entry holds the identity with the ref of its own release; the definition every version
// is generated from holds the repository the signer's workflow is in.
func TestPinSigner_packslip(t *testing.T) {
	t.Parallel()
	asset := &generate.Asset{OS: "linux", Arch: "amd64", Packslip: &aquaregistry.Packslip{
		Identity: "https://github.com/jdx/hk/.github/workflows/release.yml@refs/tags/v2.5.0",
		Issuer:   "https://token.actions.githubusercontent.com",
	}}
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	pinSigner(discardLogger(), "jdx/hk", cfg, []*version{{Version: "v2.5.0", Registry: registryOf(asset)}})
	want := &aquaregistry.Packslip{
		IdentityPrefix: "https://github.com/jdx/hk/",
		Issuer:         "https://token.actions.githubusercontent.com",
	}
	if diff := cmp.Diff(want, cfg.Packslip); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
