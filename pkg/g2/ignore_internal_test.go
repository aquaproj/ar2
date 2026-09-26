package g2

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const registryConfig = `---
# ar2's configuration for this registry. See
# https://github.com/aquaproj/ar2 for what it means.
ignored_packages:
  - name: scenarigo/scenarigo
    reason: |
      Its asset name carries the Go version the plugin support was built against.
`

// The file is spliced rather than written back, so everything that was there is there
// afterwards: the comment, the entry already in the list, and the literal block in it.
func TestIgnorePackage(t *testing.T) {
	t.Parallel()
	got, added, err := IgnorePackage(registryConfig, "foo/bar", "It is malware.")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Error("the package should have been added")
	}
	want := `---
# ar2's configuration for this registry. See
# https://github.com/aquaproj/ar2 for what it means.
ignored_packages:
  - name: foo/bar
    reason: |
      It is malware.
  - name: scenarigo/scenarigo
    reason: |
      Its asset name carries the Go version the plugin support was built against.
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// What comes back parses, and says both packages are ignored.
func TestIgnorePackage_parses(t *testing.T) {
	t.Parallel()
	got, _, err := IgnorePackage(registryConfig, "foo/bar", "It is malware.")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &RegistryConfig{}
	if err := yaml.Unmarshal([]byte(got), cfg); err != nil {
		t.Fatal(err)
	}
	ignored := cfg.Ignored()
	for _, name := range []string{"foo/bar", "scenarigo/scenarigo"} {
		if _, ok := ignored[name]; !ok {
			t.Errorf("%s isn't ignored:\n%s", name, got)
		}
	}
}

// A reason of several lines stays several lines, indented into the block.
func TestIgnorePackage_multilineReason(t *testing.T) {
	t.Parallel()
	got, _, err := IgnorePackage(registryConfig, "foo/bar", "First line.\nSecond line.\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "      First line.\n      Second line.\n  - name: scenarigo") {
		t.Errorf("the reason isn't a block:\n%s", got)
	}
}

// A package already ignored is left alone, so a removal can be finished after a failure
// without the list saying it twice.
func TestIgnorePackage_alreadyIgnored(t *testing.T) {
	t.Parallel()
	got, added, err := IgnorePackage(registryConfig, "scenarigo/scenarigo", "It is malware.")
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("it was already there")
	}
	if got != registryConfig {
		t.Errorf("the file changed:\n%s", got)
	}
}

// A registry that ignores nothing yet gets the list started.
func TestIgnorePackage_noList(t *testing.T) {
	t.Parallel()
	got, added, err := IgnorePackage("---\nbreadth:\n  versions: 5\n", "foo/bar", "It is malware.")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Error("the package should have been added")
	}
	want := "---\nbreadth:\n  versions: 5\nignored_packages:\n  - name: foo/bar\n    reason: |\n      It is malware.\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The key inside a reason isn't taken for the key itself.
func TestIgnorePackage_keyInsideAReason(t *testing.T) {
	t.Parallel()
	content := "---\nbreadth:\n  # ignored_packages: not this one\n  versions: 5\nignored_packages:\n  - name: a/b\n"
	got, _, err := IgnorePackage(content, "foo/bar", "It is malware.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ignored_packages:\n  - name: foo/bar") {
		t.Errorf("the entry went somewhere else:\n%s", got)
	}
}
