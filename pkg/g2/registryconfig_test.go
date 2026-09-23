package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// The file says what ar2 should do differently for this registry. A registry that
// asks for nothing different needs no file, so an absent one and an empty one mean
// the same thing.
func TestRegistryConfig_Ignored(t *testing.T) {
	t.Parallel()
	data := []struct {
		name    string
		content string
		exp     []string
	}{
		{
			name: "a package to leave alone",
			content: `
ignored_packages:
  - name: foo/bar
    reason: the repository was deleted
  - name: baz/qux
`,
			exp: []string{"foo/bar", "baz/qux"},
		},
		{
			name:    "nothing to leave alone",
			content: "ignored_packages: []\n",
		},
		{
			name:    "an empty file",
			content: "",
		},
		{
			// A name is what the list is read by, so an entry without one says
			// nothing and is passed over rather than matching every package.
			name: "an entry with no name",
			content: `
ignored_packages:
  - reason: somebody started writing this and stopped
`,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			cfg := &g2.RegistryConfig{}
			if err := yaml.Unmarshal([]byte(d.content), cfg); err != nil {
				t.Fatal(err)
			}
			ignored := cfg.Ignored()
			if len(ignored) != len(d.exp) {
				t.Fatalf("Ignored() has %d names, wanted %d: %v", len(ignored), len(d.exp), ignored)
			}
			for _, name := range d.exp {
				if _, ok := ignored[name]; !ok {
					t.Errorf("Ignored() doesn't hold %q: %v", name, ignored)
				}
			}
		})
	}
}

// A nil configuration is the one a registry with no file gets.
func TestRegistryConfig_Ignored_nil(t *testing.T) {
	t.Parallel()
	var cfg *g2.RegistryConfig
	if got := cfg.Ignored(); len(got) != 0 {
		t.Fatalf("Ignored() = %v, wanted nothing", got)
	}
}
