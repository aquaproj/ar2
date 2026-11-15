package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// The release's checksum files are left unchecked only when the definition turns their
// signature off.
func TestChecksumSigningOff(t *testing.T) {
	t.Parallel()
	off, on := false, true
	for _, d := range []struct {
		name string
		base *aquaregistry.PackageInfo
		want bool
	}{
		{name: "no definition"},
		{name: "no checksum", base: &aquaregistry.PackageInfo{}},
		{name: "no cosign", base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{}}},
		{name: "enabled unset", base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{Cosign: &aquaregistry.Cosign{}}}},
		{name: "enabled", base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{Cosign: &aquaregistry.Cosign{Enabled: &on}}}},
		{name: "turned off", base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{Cosign: &aquaregistry.Cosign{Enabled: &off}}}, want: true},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if got := checksumSigningOff(d.base); got != d.want {
				t.Errorf("got %t, want %t", got, d.want)
			}
		})
	}
}
