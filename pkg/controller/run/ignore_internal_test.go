package run

import (
	"log/slog"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
)

// A package the registry says to leave alone is dropped before anything asks GitHub
// about it, which is the point: it is usually on the list because its repository
// isn't there any more.
func TestIgnore(t *testing.T) {
	t.Parallel()
	candidates := []*Candidate{
		{Name: "cli/cli", Package: &state.Package{}},
		{Name: "foo/bar", Package: &state.Package{}},
		{Name: "baz/qux", Package: &state.Package{}},
	}
	data := []struct {
		name    string
		ignored []string
		exp     []string
	}{
		{
			name: "nothing ignored",
			exp:  []string{"cli/cli", "foo/bar", "baz/qux"},
		},
		{
			name:    "one ignored",
			ignored: []string{"foo/bar"},
			exp:     []string{"cli/cli", "baz/qux"},
		},
		{
			name:    "a name nothing answers to",
			ignored: []string{"not/here"},
			exp:     []string{"cli/cli", "foo/bar", "baz/qux"},
		},
		{
			name:    "a pattern",
			ignored: []string{"baz/*"},
			exp:     []string{"cli/cli", "foo/bar"},
		},
		{
			name:    "all of them",
			ignored: []string{"cli/cli", "foo/bar", "baz/qux"},
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			cfg := &g2.RegistryConfig{}
			for _, name := range d.ignored {
				cfg.IgnoredPackages = append(cfg.IgnoredPackages, &g2.IgnoredPackage{Name: name})
			}
			got := ignore(slog.New(slog.DiscardHandler), candidates, cfg)
			if len(got) != len(d.exp) {
				t.Fatalf("ignore() kept %d, wanted %d", len(got), len(d.exp))
			}
			for i, name := range d.exp {
				if got[i].Name != name {
					t.Errorf("kept[%d] is %q, wanted %q", i, got[i].Name, name)
				}
			}
		})
	}
}
