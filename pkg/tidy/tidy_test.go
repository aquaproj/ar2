package tidy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aquaproj/ar2/pkg/tidy"
	"github.com/google/go-cmp/cmp"
)

// definitionCase is one definition on disk, and what tidying it should make of it.
//
// The fixtures are files rather than strings in the table, so that what a definition looks
// like is what the test reads: a definition is a file on a package branch, and a fixture
// shortened to fit in a function stops being one.
type definitionCase struct {
	name      string
	fixture   string
	spellings []string
	checksums int
	overrides int
}

func definitionCases() []definitionCase {
	return []definitionCase{
		{
			name:      "a block of nothing but known spellings goes altogether",
			fixture:   "known",
			spellings: []string{"amd64: x86_64", "darwin: apple-darwin"},
		},
		{
			name:      "what the parser can't guess stays, and the rest goes",
			fixture:   "partial",
			spellings: []string{"amd64: x86_64"},
		},
		{
			// The override said nothing but which environment it was for once its
			// replacements went, so it went too.
			name:      "every block is reached, wherever the definition puts one",
			fixture:   "nested",
			spellings: []string{"arm64: aarch64", "windows: pc-windows-msvc"},
			overrides: 1,
		},
		{
			// aqua-registry verifies an asset against the checksum file. A generated
			// entry carries the digest of the asset, and has nowhere to say that the
			// file exists, so the definition was saying it to nobody.
			name:      "the checksum file goes, wherever the definition says it",
			fixture:   "checksum",
			checksums: 2,
		},
		{
			// What taking the last field out of an override leaves behind: it matches the
			// environment and then applies nothing to it.
			name:      "an override left saying only which environment it is for goes",
			fixture:   "empty-override",
			checksums: 1,
			overrides: 1,
		},
		{
			// One that names a variant is what makes an entry for that variant generated,
			// and one with a later sibling the same environment could match is the first
			// match rather than nothing at all.
			name:    "the overrides that are doing something stay",
			fixture: "kept-override",
		},
	}
}

func TestDefinition(t *testing.T) {
	t.Parallel()
	for _, tt := range definitionCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := fixture(t, tt.fixture+".in.yaml")
			want := fixture(t, tt.fixture+".want.yaml")
			got, removed, err := tidy.Definition(in)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("the definition is wrong (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.spellings, removed.Spellings); diff != "" {
				t.Errorf("the spellings that went are wrong (-want +got):\n%s", diff)
			}
			if removed.Checksums != tt.checksums {
				t.Errorf("%d checksum blocks went, want %d", removed.Checksums, tt.checksums)
			}
			if removed.Overrides != tt.overrides {
				t.Errorf("%d overrides went, want %d", removed.Overrides, tt.overrides)
			}
		})
	}
}

// fixture reads one of the definitions beside the test.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A spelling the parser can't read is the only thing that makes such an asset belong to a
// platform, so nothing is taken out of a definition that says only those.
func TestDefinition_keepsWhatItCantGuess(t *testing.T) {
	t.Parallel()
	in := "type: github_release\nreplacements:\n  linux: ubuntu\n"
	got, removed, err := tidy.Definition(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("the definition changed:\n%s", got)
	}
	if removed.Any() {
		t.Errorf("took out %v", removed)
	}
}

// The file is edited rather than rewritten, because the comment saying why a filter is
// there is the most valuable line in a definition and tidying up mustn't eat it.
func TestReplacements_keepsComments(t *testing.T) {
	t.Parallel()
	in := `type: github_release
# The release carries the desktop application as well.
all_assets_filter: Asset matches "^goose-"
replacements:
  amd64: x86_64
files:
  - name: goose
`
	want := `type: github_release
# The release carries the desktop application as well.
all_assets_filter: Asset matches "^goose-"
files:
  - name: goose
`
	got, removed, err := tidy.Definition(in)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the definition is wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"amd64: x86_64"}, removed.Spellings); diff != "" {
		t.Errorf("what went is wrong (-want +got):\n%s", diff)
	}
}
