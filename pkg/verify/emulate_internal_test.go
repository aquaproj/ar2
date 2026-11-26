package verify

import (
	"reflect"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
)

// Code-Hex/gqldoc v0.0.4's Windows_arm.zip is 32-bit ARM: windows/arm64 gets the x86_64
// build through emulation, as v0.0.3 had it, rather than being left out.
func TestFilled_emulate(t *testing.T) {
	t.Parallel()
	amd64 := &generate.Asset{
		OS: "windows", Arch: "amd64", Asset: "gqldoc_0.0.4_Windows_x86_64.zip", Checksum: "abc",
		Files: []*generate.File{{Name: "gqldoc", Src: "gqldoc.exe"}},
	}
	arm := &generate.Asset{OS: "windows", Arch: "arm64", Asset: "gqldoc_0.0.4_Windows_arm.zip"}
	linuxArm := &generate.Asset{OS: "linux", Arch: "arm64", Asset: "gqldoc_0.0.4_Linux_arm.tar.gz"}
	linux := &generate.Asset{OS: "linux", Arch: "amd64", Asset: "gqldoc_0.0.4_Linux_x86_64.tar.gz"}
	f := &Filled{}
	f.wrongArch(arm, errNotArm64)
	f.wrongArch(linuxArm, errNotArm64)

	kept := f.emulate(discardLogger(), []*generate.Asset{amd64, linux})

	if len(kept) != 3 {
		t.Fatalf("the file keeps %d entries: %+v", len(kept), kept)
	}
	want := *amd64
	want.Arch = "arm64"
	want.WindowsARMEmulation = true
	if got := kept[2]; !reflect.DeepEqual(*got, want) {
		t.Errorf("the emulated entry is %+v, want %+v", got, want)
	}
	if amd64.Arch != "amd64" || amd64.WindowsARMEmulation {
		t.Errorf("the amd64 entry was changed: %+v", amd64)
	}
	if len(f.Excluded) != 1 || f.Excluded[0].Asset != linuxArm {
		t.Errorf("what went is %+v", f.Excluded)
	}
}

// An OS with no amd64 entry kept has nothing to emulate, and the environment stays out.
func TestFilled_emulate_noAmd64(t *testing.T) {
	t.Parallel()
	arm := &generate.Asset{OS: "darwin", Arch: "arm64", Asset: "foo_darwin_arm.tar.gz"}
	f := &Filled{}
	f.wrongArch(arm, errNotArm64)
	kept := f.emulate(discardLogger(), nil)
	if len(kept) != 0 {
		t.Errorf("the file keeps %+v", kept)
	}
	if len(f.Excluded) != 1 {
		t.Errorf("what went is %+v", f.Excluded)
	}
}

func TestEmulates(t *testing.T) {
	t.Parallel()
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		if got := emulates(goos); got != want {
			t.Errorf("%s: got %t", goos, got)
		}
	}
}
