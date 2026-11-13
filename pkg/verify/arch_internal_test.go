package verify

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
)

// elfHeader is the smallest ELF file debug/elf reads: a header and nothing else.
func elfHeader(t *testing.T, class64 bool, machine uint16) []byte {
	t.Helper()
	size := 52
	if class64 {
		size = 64
	}
	b := make([]byte, size)
	copy(b, "\x7fELF")
	b[4] = 1 // ELFCLASS32
	if class64 {
		b[4] = 2
	}
	b[5] = 1                                 // little endian
	b[6] = 1                                 // EV_CURRENT
	binary.LittleEndian.PutUint16(b[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(b[18:], machine)
	binary.LittleEndian.PutUint32(b[20:], 1)
	return b
}

func writeFile(t *testing.T, dir, name string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
}

func TestAmbiguousArm(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"mkcert-v1.4.1-linux-arm":          true,
		"volta-2.0.2-linux-arm.tar.gz":     true,
		"ghost-linux-armv5-2.11.3.gz":      true,
		"bob-linux-arm64.zip":              false,
		"tool_linux_aarch64.tar.gz":        false,
		"tool_linux_amd64.tar.gz":          false,
		"kubectx_v0.9.4_windows_armhf.zip": true,
		"Xray-windows-arm64-v8a.zip":       false,
		"tool-linux-armv8l.tar.gz":         true,
	} {
		if got := ambiguousArm(name); got != want {
			t.Errorf("ambiguousArm(%q) = %v, want %v", name, got, want)
		}
	}
}

// FiloSottile/mkcert's linux-arm is 32-bit ARM, which an arm64 machine can't run, and
// volta-cli/volta's is aarch64.
func TestCheckArm64(t *testing.T) {
	t.Parallel()
	const emARM, emAArch64 = 40, 183
	for _, tt := range []struct {
		name  string
		asset string
		arch  string
		bin   []byte
		want  error
	}{
		{name: "32-bit ARM", asset: "mkcert-v1.4.1-linux-arm", arch: "arm64", bin: elfHeader(t, false, emARM), want: errNotArm64},
		{name: "aarch64 named arm", asset: "volta-2.0.2-linux-arm.tar.gz", arch: "arm64", bin: elfHeader(t, true, emAArch64)},
		{name: "the name says arm64", asset: "tool-linux-arm64", arch: "arm64", bin: elfHeader(t, false, emARM)},
		{name: "not an arm64 entry", asset: "tool-linux-arm", arch: "amd64", bin: elfHeader(t, false, emARM)},
		{name: "a script", asset: "tool-linux-arm.tar.gz", arch: "arm64", bin: []byte("#!/bin/sh\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir, "tool", tt.bin)
			err := checkArm64(dir, &generate.Asset{OS: "linux", Arch: tt.arch, Asset: tt.asset}, []*generate.File{{Name: "tool"}})
			if !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// machoHeader is the smallest 64-bit Mach-O file debug/macho reads: a header with no load
// commands.
func machoHeader(cpu uint32) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[0:], 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:], cpu)
	binary.LittleEndian.PutUint32(b[12:], 2) // MH_EXECUTE
	return b
}

// fatMacho is a universal Mach-O file holding a thin one for each CPU.
func fatMacho(cpus ...uint32) []byte {
	const align = 12 // 4096
	header := 8 + 20*len(cpus)
	offset := (header + 1<<align - 1) &^ (1<<align - 1)
	b := make([]byte, offset+len(cpus)<<align)
	binary.BigEndian.PutUint32(b[0:], 0xcafebabe)
	binary.BigEndian.PutUint32(b[4:], uint32(len(cpus))) //nolint:gosec
	for i, cpu := range cpus {
		at := offset + i<<align
		arch := b[8+20*i:]
		binary.BigEndian.PutUint32(arch[0:], cpu)
		binary.BigEndian.PutUint32(arch[8:], uint32(at)) //nolint:gosec
		binary.BigEndian.PutUint32(arch[12:], 32)
		binary.BigEndian.PutUint32(arch[16:], align)
		copy(b[at:], machoHeader(cpu))
	}
	return b
}

// AOMediaCodec/libavif's macOS-artifacts.zip names no arch and holds arm64 executables,
// which an Intel Mac can't run.
func TestCheckAmd64(t *testing.T) {
	t.Parallel()
	const (
		emX8664, emAArch64 = 62, 183
		cpuAmd64, cpuArm64 = 0x01000007, 0x0100000c
	)
	for _, tt := range []struct {
		name string
		arch string
		bin  []byte
		want error
	}{
		{name: "arm64 Mach-O", arch: "amd64", bin: machoHeader(cpuArm64), want: errNotAmd64},
		{name: "x86_64 Mach-O", arch: "amd64", bin: machoHeader(cpuAmd64)},
		{name: "universal", arch: "amd64", bin: fatMacho(cpuArm64, cpuAmd64)},
		{name: "universal of arm64 alone", arch: "amd64", bin: fatMacho(cpuArm64), want: errNotAmd64},
		{name: "aarch64 ELF", arch: "amd64", bin: elfHeader(t, true, emAArch64), want: errNotAmd64},
		{name: "x86-64 ELF", arch: "amd64", bin: elfHeader(t, true, emX8664)},
		{name: "not an amd64 entry", arch: "arm64", bin: machoHeader(cpuArm64)},
		{name: "a script", arch: "amd64", bin: []byte("#!/bin/sh\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir, "tool", tt.bin)
			err := checkAmd64(dir, &generate.Asset{OS: "darwin", Arch: tt.arch, Asset: "macOS-artifacts.zip"}, []*generate.File{{Name: "tool"}})
			if !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}
