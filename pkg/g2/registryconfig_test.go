package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// The file says what ar2 should do differently for this registry. A registry that
// asks for nothing different needs no file, so an absent one and an empty one mean
// the same thing.
func TestRegistryConfig_Ignores(t *testing.T) { //nolint:funlen
	t.Parallel()
	data := []struct {
		name    string
		content string
		ignored []string
		kept    []string
	}{
		{
			name: "a package to leave alone",
			content: `
ignored_packages:
  - name: foo/bar
    reason: the repository was deleted
  - name: baz/qux
`,
			ignored: []string{"foo/bar", "baz/qux"},
			kept:    []string{"foo/baz", "cli/cli"},
		},
		{
			// An organization whose IP allow list refuses the API to the runners: every
			// package of it is left alone by one entry.
			name: "every package of an organization",
			content: `
ignored_packages:
  - name: aquasecurity/*
`,
			ignored: []string{"aquasecurity/trivy", "aquasecurity/tfsec"},
			kept:    []string{"aquasecurity/trivy/foo", "aquasecurity", "cli/cli"},
		},
		{
			// A pattern path.Match can't read still leaves alone the package of its
			// own spelling, which is what it did before the list took patterns.
			name: "a malformed pattern",
			content: `
ignored_packages:
  - name: foo/[bar
`,
			ignored: []string{"foo/[bar"},
			kept:    []string{"foo/bar"},
		},
		{
			name:    "nothing to leave alone",
			content: "ignored_packages: []\n",
			kept:    []string{"cli/cli"},
		},
		{
			name:    "an empty file",
			content: "",
			kept:    []string{"cli/cli"},
		},
		{
			// A name is what the list is read by, so an entry without one says
			// nothing and is passed over rather than matching every package.
			name: "an entry with no name",
			content: `
ignored_packages:
  - reason: somebody started writing this and stopped
`,
			kept: []string{"cli/cli"},
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			cfg := &g2.RegistryConfig{}
			if err := yaml.Unmarshal([]byte(d.content), cfg); err != nil {
				t.Fatal(err)
			}
			for _, name := range d.ignored {
				if !cfg.Ignores(name) {
					t.Errorf("%s isn't ignored", name)
				}
			}
			for _, name := range d.kept {
				if cfg.Ignores(name) {
					t.Errorf("%s is ignored", name)
				}
			}
		})
	}
}

// A nil configuration is the one a registry with no file gets.
func TestRegistryConfig_Ignores_nil(t *testing.T) {
	t.Parallel()
	var cfg *g2.RegistryConfig
	if cfg.Ignores("cli/cli") {
		t.Fatal("a nil configuration ignores cli/cli")
	}
}
