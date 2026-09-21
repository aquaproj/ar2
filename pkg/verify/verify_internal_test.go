package verify

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/generate"
)

// newArchive writes the given paths as empty files and returns the directory,
// standing in for an extracted archive.
func newArchive(t *testing.T, paths ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestResolveFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		archive     []string
		files       []*generate.File
		want        []*generate.File
		needsReview bool
		unresolved  []string
	}{
		{
			// The common case: nothing moved, so the registry's template is carried
			// over and the pull request can merge on its own.
			name:    "every src is present",
			archive: []string{"gh_2.1.0_linux_amd64/bin/gh"},
			files:   []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
			want:    []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
		},
		{
			// The upstream reorganized the archive. The executable is found by name
			// and relocated, but relocating is a guess, so it has to be reviewed.
			name:        "src moved",
			archive:     []string{"bin/gh"},
			files:       []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
			want:        []*generate.File{{Name: "gh", Src: "bin/gh"}},
			needsReview: true,
		},
		{
			// Windows executables carry an extension the registry's name doesn't.
			name:        "src moved to a windows executable",
			archive:     []string{"bin/gh.exe"},
			files:       []*generate.File{{Name: "gh", Src: "gh_2.1.0_windows_amd64/bin/gh.exe"}},
			want:        []*generate.File{{Name: "gh", Src: "bin/gh.exe"}},
			needsReview: true,
		},
		{
			// Nothing in the archive carries the name, so there is nothing to guess.
			name:        "the executable is gone",
			archive:     []string{"README.md"},
			files:       []*generate.File{{Name: "gh", Src: "bin/gh"}},
			want:        []*generate.File{{Name: "gh", Src: "bin/gh"}},
			needsReview: true,
			unresolved:  []string{"gh"},
		},
		{
			// A raw asset has no src; the name alone locates it.
			name:    "no src",
			archive: []string{"gh"},
			files:   []*generate.File{{Name: "gh"}},
			want:    []*generate.File{{Name: "gh"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := newArchive(t, tt.archive...)
			files, needsReview, unresolved := resolveFiles(discardLogger(), dir, tt.files)
			if diff := cmp.Diff(tt.want, files); diff != "" {
				t.Errorf("files are wrong (-want +got):\n%s", diff)
			}
			if needsReview != tt.needsReview {
				t.Errorf("needsReview is %v, want %v", needsReview, tt.needsReview)
			}
			if diff := cmp.Diff(tt.unresolved, unresolved); diff != "" {
				t.Errorf("unresolved is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

// TestIndexByName checks that a name appearing twice resolves to the shallower path,
// which is where an archive normally puts the command rather than a copy of it.
func TestIndexByName(t *testing.T) {
	t.Parallel()
	dir := newArchive(t, "share/doc/gh", "bin/gh")
	index, err := indexByName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("bin/gh", index["gh"]); diff != "" {
		t.Errorf("indexByName is wrong (-want +got):\n%s", diff)
	}
}
