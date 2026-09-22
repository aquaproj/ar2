package verify

import (
	"debug/elf"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
)

// The libc an executable is linked against, as recorded in registry.json.
const (
	libcMusl   = "musl"
	libcGlibc  = "glibc"
	libcStatic = "static"
)

// linkedLibc reports the libc the executables in an extracted asset need.
//
// It is read from the binaries rather than guessed from the asset's name. An ELF
// executable names its dynamic linker in a PT_INTERP header, and that path says which
// libc it is: /lib/ld-musl-* is musl, /lib64/ld-linux-* is glibc. An executable with
// no PT_INTERP is statically linked and needs neither.
//
// The answer is empty when nothing can be read that way, which covers every non-Linux
// asset as well as one holding a script or a jar. Recording nothing is right there:
// the field says what was read, and an asset it can't read is not an asset that needs
// no libc.
func linkedLibc(logger *slog.Logger, dir string, files []*generate.File) string {
	libc := ""
	for _, file := range files {
		src := file.Src
		if src == "" {
			src = file.Name
		}
		read := readLinkedLibc(logger, filepath.Join(dir, src))
		if read == "" {
			continue
		}
		switch {
		case libc == "":
			libc = read
		case libc == read:
		case libc == libcStatic:
			// One executable needing a libc makes the asset need it, however the
			// others are linked.
			libc = read
		case read == libcStatic:
		default:
			// One binary needs musl and another needs glibc. No single value
			// describes the asset, and guessing which matters would be worse than
			// saying nothing.
			logger.Warn("the executables in the asset are linked against different libc",
				"libc", libc, "other_libc", read, "file", src)
			return ""
		}
	}
	return libc
}

func readLinkedLibc(logger *slog.Logger, path string) string {
	f, err := elf.Open(path)
	if err != nil {
		// Not an ELF file: a Mach-O or PE executable, a script, or something that
		// isn't an executable at all.
		logger.Debug("the file isn't ELF", "file", path, "error", err.Error())
		return ""
	}
	defer f.Close()

	for _, prog := range f.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		b := make([]byte, prog.Filesz)
		if _, err := prog.ReadAt(b, 0); err != nil {
			logger.Debug("failed to read the interpreter", "file", path, "error", err.Error())
			return ""
		}
		interp := strings.TrimRight(string(b), "\x00")
		switch {
		case strings.Contains(interp, "musl"):
			return libcMusl
		case strings.Contains(interp, "ld-linux"):
			return libcGlibc
		default:
			logger.Debug("the interpreter isn't recognised", "file", path, "interpreter", interp)
			return ""
		}
	}
	// No interpreter: nothing is loaded at run time, so no libc is needed.
	return libcStatic
}
