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
		ignored []*g2.IgnoredPackage
		exp     []string
	}{
		{
			name: "nothing ignored",
			exp:  []string{"cli/cli", "foo/bar", "baz/qux"},
		},
		{
			name:    "one ignored",
			ignored: []*g2.IgnoredPackage{{Name: "foo/bar"}},
			exp:     []string{"cli/cli", "baz/qux"},
		},
		{
			name:    "a name nothing answers to",
			ignored: []*g2.IgnoredPackage{{Name: "not/here"}},
			exp:     []string{"cli/cli", "foo/bar", "baz/qux"},
		},
		{
			name:    "a regular expression",
			ignored: []*g2.IgnoredPackage{{NameRegexp: "baz/.*"}},
			exp:     []string{"cli/cli", "foo/bar"},
		},
		{
			name:    "all of them",
			ignored: []*g2.IgnoredPackage{{Name: "cli/cli"}, {Name: "foo/bar"}, {Name: "baz/qux"}},
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			got := ignore(slog.New(slog.DiscardHandler), candidates, &g2.RegistryConfig{IgnoredPackages: d.ignored})
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

// A package a run found served as a template leaves the order, so no sweep asks about it.
func TestWithoutTemplates(t *testing.T) {
	t.Parallel()
	got := withoutTemplates(slog.New(slog.DiscardHandler), []*Candidate{
		{Name: "cli/cli", Package: &state.Package{}},
		{Name: "1password/cli", Package: &state.Package{Template: true}},
	})
	if len(got) != 1 || got[0].Name != "cli/cli" {
		t.Errorf("got %v", got)
	}
}

// A run naming a package served as a template still reaches it, which is how a definition
// that no longer declares a template is noticed.
func TestCandidates_template(t *testing.T) {
	t.Parallel()
	s := state.New()
	s.Packages["1password/cli"] = &state.Package{Template: true}
	s.Packages["cli/cli"] = &state.Package{RepoOwner: "cli", RepoName: "cli"}
	c := &Controller{}
	logger := slog.New(slog.DiscardHandler)
	names := func(cs []*Candidate) []string {
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	if got := names(c.candidates(logger, &Input{State: s}, nil)); len(got) != 1 || got[0] != "cli/cli" {
		t.Errorf("the order: got %v, want cli/cli alone", got)
	}
	if got := names(c.candidates(logger, &Input{State: s, Packages: []string{"1password/cli"}}, nil)); len(got) != 1 || got[0] != "1password/cli" {
		t.Errorf("a named run: got %v, want 1password/cli", got)
	}
}
