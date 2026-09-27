package tidy_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/tidy"
	"github.com/google/go-cmp/cmp"
)

// definitionCase is one definition, and what tidying it should make of it.
type definitionCase struct {
	name    string
	in      string
	want    string
	removed []string
}

func definitionCases() []definitionCase {
	return []definitionCase{
		{
			name: "a block of nothing but known spellings goes altogether",
			in: `type: github_release
repo_owner: astral-sh
replacements:
  amd64: x86_64
  darwin: apple-darwin
files:
  - name: uv
`,
			want: `type: github_release
repo_owner: astral-sh
files:
  - name: uv
`,
			removed: []string{"amd64: x86_64", "darwin: apple-darwin"},
		},
		{
			name: "what the parser can't guess stays, and the rest goes",
			in: `type: github_release
repo_owner: luau-lang
replacements:
  amd64: x86_64
  linux: ubuntu
`,
			want: `type: github_release
repo_owner: luau-lang
replacements:
  linux: ubuntu
`,
			removed: []string{"amd64: x86_64"},
		},
		{
			name: "every block is reached, wherever the definition puts one",
			in: `type: github_release
version_overrides:
  - version_constraint: "true"
    replacements:
      arm64: aarch64
      linux: ubuntu
    overrides:
      - goos: windows
        replacements:
          windows: pc-windows-msvc
`,
			want: `type: github_release
version_overrides:
  - version_constraint: "true"
    replacements:
      linux: ubuntu
    overrides:
      - goos: windows
`,
			removed: []string{"arm64: aarch64", "windows: pc-windows-msvc"},
		},
	}
}

func TestReplacements(t *testing.T) {
	t.Parallel()
	for _, tt := range definitionCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, removed, err := tidy.Replacements(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the definition is wrong (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.removed, removed); diff != "" {
				t.Errorf("what went is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

// A spelling the parser can't read is the only thing that makes such an asset belong to a
// platform, so nothing is taken out of a definition that says only those.
func TestReplacements_keepsWhatItCantGuess(t *testing.T) {
	t.Parallel()
	in := "type: github_release\nreplacements:\n  linux: ubuntu\n"
	got, removed, err := tidy.Replacements(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("the definition changed:\n%s", got)
	}
	if len(removed) != 0 {
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
	got, removed, err := tidy.Replacements(in)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the definition is wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"amd64: x86_64"}, removed); diff != "" {
		t.Errorf("what went is wrong (-want +got):\n%s", diff)
	}
}
