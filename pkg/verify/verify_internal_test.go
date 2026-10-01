package verify

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/google/go-cmp/cmp"
)

// newArchive writes the given paths as empty files and returns the directory, standing in
// for an extracted archive. A path in executables is written as a file that can be run,
// which is what an archive records for a command.
func newArchive(t *testing.T, paths []string, executables ...string) string {
	t.Helper()
	runnable := make(map[string]struct{}, len(executables))
	for _, p := range executables {
		runnable[p] = struct{}{}
	}
	dir := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if _, ok := runnable[p]; ok {
			mode = 0o700
		}
		if err := os.WriteFile(full, nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// resolveCase is one extracted archive, and what resolving the definition's files against it
// should make of them.
type resolveCase struct {
	name        string
	goos        string
	archive     []string
	files       []*generate.File
	want        []*generate.File
	needsReview bool
	unresolved  []string
	// executables are the archive's paths that can be run, which is how a command is
	// told from a file named after one.
	executables []string
}

func resolveCases() []resolveCase {
	return append(resolvedCases(), guessedCases()...)
}

// resolvedCases are the archives that answer for themselves: the definition's files are
// where it says, or are the same files elsewhere in the archive.
func resolvedCases() []resolveCase {
	return []resolveCase{
		{
			// The common case: nothing moved, so the registry's template is carried
			// over and the pull request can merge on its own.
			name:    "every src is present",
			archive: []string{"gh_2.1.0_linux_amd64/bin/gh"},
			files:   []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
			want:    []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
		},
		{
			// The upstream reorganized the archive: it dropped the directory it used to
			// pack everything under. The file is the same file under the same name, so
			// recording where it is now says what the definition said and the pull
			// request can merge on its own.
			name:    "src moved",
			archive: []string{"bin/gh"},
			files:   []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
			want:    []*generate.File{{Name: "gh", Src: "bin/gh"}},
		},
		{
			// Windows executables carry an extension the registry's name doesn't.
			name:    "src moved to a windows executable",
			goos:    "windows",
			archive: []string{"bin/gh.exe"},
			files:   []*generate.File{{Name: "gh", Src: "gh_2.1.0_windows_amd64/bin/gh.exe"}},
			want:    []*generate.File{{Name: "gh", Src: "bin/gh.exe"}},
		},
		{
			// aqua resolves files[].src with the Windows extension added, and renames
			// the extracted file to match when it installs it. The entry has to name
			// the file the archive holds, since nothing has been renamed yet.
			name:    "the archive holds the name without the windows extension",
			goos:    "windows",
			archive: []string{"tree-sitter-windows-x64"},
			files:   []*generate.File{{Name: "tree-sitter", Src: "tree-sitter-windows-x64.exe"}},
			want:    []*generate.File{{Name: "tree-sitter", Src: "tree-sitter-windows-x64"}},
		},
		{
			// The archive does carry the extension, so the resolved src is what it says.
			name:    "the archive holds the windows executable",
			goos:    "windows",
			archive: []string{"gh_2.1.0_windows_amd64/bin/gh.exe"},
			files:   []*generate.File{{Name: "gh", Src: "gh_2.1.0_windows_amd64/bin/gh.exe"}},
			want:    []*generate.File{{Name: "gh", Src: "gh_2.1.0_windows_amd64/bin/gh.exe"}},
		},
		{
			// A release can ship a file of the command's name that isn't the command:
			// git-bug's archive holds git-bug.exe beside a completion script called
			// git-bug. On Windows the executable is the one with the extension.
			name:    "a windows archive holds the name and the executable",
			goos:    "windows",
			archive: []string{"git-bug_0.11.0_windows_amd64/git-bug.exe", "git-bug_0.11.0_windows_amd64/completion/bash/git-bug"},
			files:   []*generate.File{{Name: "git-bug", Src: "git-bug.exe"}},
			want:    []*generate.File{{Name: "git-bug", Src: "git-bug_0.11.0_windows_amd64/git-bug.exe"}},
		},
		{
			// A raw asset has no src; the name alone locates it.
			name:    "no src",
			archive: []string{"gh"},
			files:   []*generate.File{{Name: "gh"}},
			want:    []*generate.File{{Name: "gh"}},
		},
		{
			// A release that ships a completion script named after its command: CMake's
			// archive holds bin/cmake and share/bash-completion/completions/cmake. The
			// script isn't executable, so the archive says which is the command and
			// nothing is being guessed.
			name:        "a completion script carries the name too",
			archive:     []string{"CMake.app/Contents/bin/cmake", "CMake.app/Contents/share/bash-completion/completions/cmake"},
			executables: []string{"CMake.app/Contents/bin/cmake"},
			files:       []*generate.File{{Name: "cmake", Src: "cmake-4.4.3-macos-universal/CMake.app/Contents/bin/cmake"}},
			want:        []*generate.File{{Name: "cmake", Src: "CMake.app/Contents/bin/cmake"}},
		},
	}
}

// guessedCases are the archives where resolving a file takes a guess, which is what a
// human is asked to look at.
func guessedCases() []resolveCase {
	return []resolveCase{
		{
			// Two files of that name, and the name can't say which of them is the
			// command. The shallower one is recorded, because that is where an archive
			// normally puts it, and a human is asked whether it is the right one.
			name:        "the archive holds the name twice",
			archive:     []string{"share/doc/gh", "bin/gh"},
			files:       []*generate.File{{Name: "gh", Src: "gh_2.1.0_linux_amd64/bin/gh"}},
			want:        []*generate.File{{Name: "gh", Src: "bin/gh"}},
			needsReview: true,
		},
		{
			// The definition named a file the archive doesn't hold, and what was found
			// is a file of another name. Whether that is the same command is a guess.
			name:        "found under another name",
			archive:     []string{"gh"},
			files:       []*generate.File{{Name: "gh", Src: "bin/gh-linux-amd64"}},
			want:        []*generate.File{{Name: "gh", Src: "gh"}},
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
	}
}

func TestResolveFiles(t *testing.T) {
	t.Parallel()
	for _, tt := range resolveCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := newArchive(t, tt.archive, tt.executables...)
			files, needsReview, unresolved, holds := resolveFiles(discardLogger(), dir, tt.goos, tt.files)
			if diff := cmp.Diff(tt.want, files); diff != "" {
				t.Errorf("files are wrong (-want +got):\n%s", diff)
			}
			if needsReview != tt.needsReview {
				t.Errorf("needsReview is %v, want %v", needsReview, tt.needsReview)
			}
			if diff := cmp.Diff(tt.unresolved, unresolved); diff != "" {
				t.Errorf("unresolved is wrong (-want +got):\n%s", diff)
			}
			checkHolds(t, unresolved, holds, tt.archive)
		})
	}
}

// TestIndexByName checks that a name appearing twice keeps both paths, shallowest
// first: the first one is where an archive normally puts the command rather than a copy
// of it, and that there is a second one at all is what makes choosing it a guess.
func TestIndexByName(t *testing.T) {
	t.Parallel()
	dir := newArchive(t, []string{"share/doc/gh", "bin/gh"}, "bin/gh")
	index, err := indexByName(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []*found{{Path: "bin/gh", Executable: true}, {Path: "share/doc/gh"}}
	if diff := cmp.Diff(want, index["gh"]); diff != "" {
		t.Errorf("indexByName is wrong (-want +got):\n%s", diff)
	}
}

// Whether an asset can be opened is a question about this machine, not about the
// package. Everything unpacked in process can always be opened; dmg and pkg need a
// macOS tool.
func TestExtractable(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"tar.gz", "zip", "raw", ""} {
		if !Extractable(format) {
			t.Errorf("%q should always be extractable", format)
		}
	}
	// The result depends on where the test runs, so what is checked is that the
	// answer follows the tool rather than the format.
	for format, tool := range formatTools {
		_, err := exec.LookPath(tool)
		if got, want := Extractable(format), err == nil; got != want {
			t.Errorf("%q is %v, want %v: %s is %v", format, got, want, tool, err)
		}
	}
}

// checkHolds asserts what the archive holds is said when, and only when, something couldn't be
// resolved -- which is the only time anybody needs it.
func checkHolds(t *testing.T, unresolved, holds, archive []string) {
	t.Helper()
	if len(unresolved) == 0 && len(holds) > 0 {
		t.Errorf("said what the archive holds with nothing unresolved: %v", holds)
	}
	if len(unresolved) > 0 && len(holds) == 0 && len(archive) > 0 {
		t.Error("something was unresolved and it didn't say what the archive holds")
	}
}
