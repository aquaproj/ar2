package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

func envsOf(assets []*g2.Asset) []string {
	out := make([]string, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.OS+"/"+a.Arch)
	}
	return out
}

// An environment the definition's supported_envs leaves out isn't generated, and an arm64
// machine running the amd64 build under emulation counts as amd64.
func TestDefinedEnvs(t *testing.T) {
	t.Parallel()
	all := []*g2.Asset{
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
		{OS: "windows", Arch: "arm64"},
	}
	data := []struct {
		name    string
		pkgInfo *aquaregistry.PackageInfo
		base    *aquaregistry.PackageInfo
		want    []string
	}{
		{
			// kcp-dev/kcp: the Windows release holds only the kubectl plugins.
			name:    "darwin and linux",
			pkgInfo: &aquaregistry.PackageInfo{},
			base:    &aquaregistry.PackageInfo{SupportedEnvs: []string{"darwin", "linux"}},
			want:    []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"},
		},
		{
			// windows/arm64 runs the amd64 build, which the inference says.
			name:    "amd64 under emulation",
			pkgInfo: &aquaregistry.PackageInfo{WindowsARMEmulation: true},
			base:    &aquaregistry.PackageInfo{SupportedEnvs: []string{"darwin", "linux", "amd64"}},
			want:    []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"},
		},
		{
			name:    "nothing said",
			pkgInfo: &aquaregistry.PackageInfo{},
			base:    &aquaregistry.PackageInfo{},
			want:    envsOf(all),
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			got := envsOf(definedEnvs(all, d.pkgInfo, d.base))
			if diff := cmp.Diff(d.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
