package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
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

// Of the definition's checksum.cosign.opts, only the ones the release can't tell are taken: a
// signer or a certificate named there is left to what the release says.
func TestUninferableOpts(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		name string
		base *aquaregistry.PackageInfo
		want []string
	}{
		{name: "no definition"},
		{name: "no cosign", base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{}}},
		{
			name: "only the ones nothing in the release says",
			base: &aquaregistry.PackageInfo{Checksum: &aquaregistry.Checksum{Cosign: &aquaregistry.Cosign{Opts: []string{
				"--certificate-identity", "https://example.com/someone",
				"--insecure-ignore-sct",
				"--certificate", "https://example.com/checksums.txt.pem",
				"--insecure-ignore-tlog",
			}}}},
			want: []string{"--insecure-ignore-sct", "--insecure-ignore-tlog"},
		},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(d.want, uninferableOpts(d.base)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
