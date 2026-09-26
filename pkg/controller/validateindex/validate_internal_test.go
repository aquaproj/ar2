package validateindex

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) { //nolint:funlen
	t.Parallel()
	data := []struct {
		name    string
		content string
		wantErr bool
		// says is what the report has to name, for the failures worth explaining.
		says string
	}{
		{
			name:    "a catalogue",
			content: `{"packages":[{"name":"cli/cli","description":"GitHub's official command line tool"}]}`,
		},
		{
			// A registry with no packages yet is a registry, not a broken file.
			name:    "empty",
			content: `{"packages":[]}`,
		},
		{
			name:    "not JSON",
			content: "packages:\n  - name: cli/cli\n",
			wantErr: true,
		},
		{
			name:    "no packages list",
			content: `{}`,
			wantErr: true,
		},
		{
			// aqua would ignore it, so the file doesn't mean what it says.
			name:    "a field aqua doesn't read",
			content: `{"packages":[{"name":"cli/cli","descriptions":"typo"}]}`,
			wantErr: true,
		},
		{
			name:    "an entry with no name",
			content: `{"packages":[{"description":"nameless"}]}`,
			wantErr: true,
			says:    "packages[0] has no name",
		},
		{
			// Shown twice by a search, and the second one is never explained.
			name:    "a package listed twice",
			content: `{"packages":[{"name":"cli/cli"},{"name":"cli/cli"}]}`,
			wantErr: true,
			says:    "cli/cli is listed more than once",
		},
		{
			name:    "two documents",
			content: `{"packages":[]}{"packages":[]}`,
			wantErr: true,
		},
		{
			// An alias is how someone whose aqua.yaml still says the old name reaches
			// the package. The table that resolves it needs one answer per name.
			name:    "an alias",
			content: `{"packages":[{"name":"anomalyco/opencode","aliases":["sst/opencode"]}]}`,
		},
		{
			name:    "an alias that is a package of its own",
			content: `{"packages":[{"name":"c/d","aliases":["a/b"]},{"name":"a/b"}]}`,
			wantErr: true,
			says:    "a/b is an alias of c/d and a package of its own",
		},
		{
			name:    "an alias claimed twice",
			content: `{"packages":[{"name":"c/d","aliases":["a/b"]},{"name":"e/f","aliases":["a/b"]}]}`,
			wantErr: true,
			says:    "a/b is an alias of both c/d and e/f",
		},
		{
			name:    "a package aliasing itself",
			content: `{"packages":[{"name":"c/d","aliases":["c/d"]}]}`,
			wantErr: true,
			says:    "c/d is its own alias",
		},
		{
			name:    "an alias with no name",
			content: `{"packages":[{"name":"c/d","aliases":[""]}]}`,
			wantErr: true,
			says:    "c/d has an alias with no name",
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "index.json")
			if err := os.WriteFile(path, []byte(d.content), 0o600); err != nil {
				t.Fatal(err)
			}
			out := &strings.Builder{}
			err := Validate(out, path)
			if !d.wantErr {
				if err != nil {
					t.Fatalf("Validate(): %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() wanted an error")
			}
			if d.says != "" && !strings.Contains(out.String(), d.says) {
				t.Fatalf("the report doesn't say %q:\n%s", d.says, out.String())
			}
		})
	}
}

func TestValidate_missingFile(t *testing.T) {
	t.Parallel()
	if err := Validate(io.Discard, filepath.Join(t.TempDir(), "index.json")); err == nil {
		t.Fatal("Validate() wanted an error for a file that isn't there")
	}
}
