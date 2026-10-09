package validatedefinition

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// at writes the definition to a file and returns its path.
func at(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The definition nodejs/node was left with: the replacements went into the middle of a
// block scalar, so it stopped being YAML. This is the check that was missing.
func TestValidate_notYAML(t *testing.T) {
	t.Parallel()
	// The file as it was merged. What breaks it is further down than the misplacement:
	// the prose folds into the last value, and the paragraph after the blank line is
	// then indented into nothing a mapping can hold.
	err := Validate(io.Discard, at(t, `name: nodejs/node
type: http
repo_owner: nodejs
repo_name: node
description: |
replacements:
  windows: win
  amd64: x64
    Node.js JavaScript runtime

    ## How to set up

    Please see https://aquaproj.github.io/docs/reference/nodejs-support
version_overrides:
    - version_constraint: "true"
      url: https://nodejs.org/dist/{{.Version}}/node.tar.gz
`))
	if err == nil {
		t.Fatal("a definition that isn't YAML should be refused")
	}
	if !strings.Contains(err.Error(), "as YAML") {
		t.Errorf("the error is %v", err)
	}
}

// A field aqua wouldn't read is how a definition stops saying what it looks like it says.
func TestValidate_unknownField(t *testing.T) {
	t.Parallel()
	err := Validate(io.Discard, at(t, "name: cli/cli\ntype: github_release\nreplacments:\n  amd64: x64\n"))
	if err == nil {
		t.Fatal("a definition with a field aqua wouldn't read should be refused")
	}
}

// The name is the only thing that says which package the branch holds.
func TestValidate_noName(t *testing.T) {
	t.Parallel()
	err := Validate(io.Discard, at(t, "type: github_release\nrepo_owner: cli\nrepo_name: cli\n"))
	if !errors.Is(err, errNoName) {
		t.Errorf("the error is %v", err)
	}
}

// A definition that names a package and says nothing else about where it comes from isn't
// one to generate from -- unless it is the branch's claim, which says the name alone.
func TestValidate_noType(t *testing.T) {
	t.Parallel()
	err := Validate(io.Discard, at(t, "name: cli/cli\nrepo_owner: cli\nrepo_name: cli\n"))
	if !errors.Is(err, errNoType) {
		t.Errorf("the error is %v", err)
	}
}

// A branch created and not yet taken over holds its claim to the package, which is right
// to say no more.
func TestValidate_claim(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := Validate(&out, at(t, "name: cli/cli\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claim") {
		t.Errorf("what it said is %q", out.String())
	}
}

// A definition the registry reads passes, and says what it holds.
func TestValidate(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	err := Validate(&out, at(t, `name: cli/cli
type: github_release
repo_owner: cli
repo_name: cli
description: GitHub's official command line tool
files:
  - name: gh
    src: gh_{{trimV .Version}}_{{.OS}}_{{.Arch}}/bin/gh
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "cli/cli: github_release" {
		t.Errorf("what it said is %q", got)
	}
}

// A file that isn't there is not a definition that is wrong.
func TestValidate_noFile(t *testing.T) {
	t.Parallel()
	err := Validate(io.Discard, filepath.Join(t.TempDir(), "nothing.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read the definition") {
		t.Errorf("the error is %v", err)
	}
}
