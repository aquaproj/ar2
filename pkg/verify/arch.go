package verify

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"path/filepath"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
)

// errNotArm64 is an arm64 entry whose executables are built for 32-bit ARM.
var errNotArm64 = errors.New("the executables are built for 32-bit ARM, not arm64")

// ambiguousArm says whether an asset's name says ARM without saying which.
//
// aqua's inference reads "arm" in an asset name as arm64, because some releases do
// write it that way: volta-cli/volta's linux-arm, psf/black's windows-arm.exe and
// MordechaiHadad/bob's bob-linux-arm are all 64-bit. Others mean 32-bit ARM by it --
// FiloSottile/mkcert's linux-arm, Go's GOARCH=arm -- and an arm64 machine can't run
// those. The name can't tell the two apart, so the executables are asked.
func ambiguousArm(name string) bool {
	s := strings.ToLower(name)
	if !strings.Contains(s, "arm") {
		return false
	}
	for _, sixtyFour := range []string{"arm64", "aarch64", "armv8", "arm-64", "arm_64"} {
		if strings.Contains(s, sixtyFour) {
			return false
		}
	}
	return true
}

// checkArm64 returns errNotArm64 when an executable of an arm64 entry whose asset name is
// ambiguous turns out to be built for 32-bit ARM.
//
// A file whose header can't be read -- a script, a jar -- says nothing either way, so
// only what can be read decides.
func checkArm64(dir string, asset *generate.Asset, files []*generate.File) error {
	if asset.Arch != "arm64" {
		return nil
	}
	name := asset.Asset
	if name == "" {
		name = asset.URL
	}
	if !ambiguousArm(name) {
		return nil
	}
	for _, file := range files {
		src := file.Src
		if src == "" {
			src = file.Name
		}
		if arm32(filepath.Join(dir, src)) {
			return errNotArm64
		}
	}
	return nil
}

// arm32 reports whether a file is an executable built for 32-bit ARM, reading the
// header of whichever of ELF, Mach-O or PE it is.
func arm32(path string) bool {
	if f, err := elf.Open(path); err == nil {
		defer f.Close()
		return f.Machine == elf.EM_ARM
	}
	if f, err := macho.Open(path); err == nil {
		defer f.Close()
		return f.Cpu == macho.CpuArm
	}
	if f, err := macho.OpenFat(path); err == nil {
		defer f.Close()
		for _, a := range f.Arches {
			if a.Cpu != macho.CpuArm {
				return false
			}
		}
		return len(f.Arches) > 0
	}
	if f, err := pe.Open(path); err == nil {
		defer f.Close()
		switch f.Machine {
		case pe.IMAGE_FILE_MACHINE_ARM, pe.IMAGE_FILE_MACHINE_ARMNT:
			return true
		}
	}
	return false
}
