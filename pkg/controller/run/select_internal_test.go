package run

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/state"
	"github.com/google/go-cmp/cmp"
)

// TestOrder checks the order a run works in: most starred first, and packages whose
// star count is unknown last. Sorting the latter among the zero-star packages would
// put them ahead of packages that are genuinely more used.
func TestOrder(t *testing.T) {
	t.Parallel()
	s := &state.State{
		Packages: map[string]*state.Package{
			"a/popular":   {RepoOwner: "a", RepoName: "popular", Stars: 100},
			"b/unknown":   {},
			"c/unused":    {RepoOwner: "c", RepoName: "unused", Stars: 0},
			"d/moderate":  {RepoOwner: "d", RepoName: "moderate", Stars: 10},
			"e/moderate2": {RepoOwner: "e", RepoName: "moderate2", Stars: 10},
		},
	}
	got := make([]string, 0, len(s.Packages))
	for _, c := range order(s) {
		got = append(got, c.Name)
	}
	// d and e tie on stars and are ordered by name, so that the same state always
	// produces the same work in the same order.
	want := []string{"a/popular", "d/moderate", "e/moderate2", "c/unused", "b/unknown"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("order is wrong (-want +got):\n%s", diff)
	}
}

// TestOrder_rounds checks that a package the last run reached goes behind the ones it
// didn't, whatever its stars. A run is bounded, so without this the packages behind
// the cut wait on the ones in front finishing -- and one that takes a share and gets
// nowhere never finishes.
func TestOrder_rounds(t *testing.T) {
	t.Parallel()
	s := &state.State{
		Packages: map[string]*state.Package{
			// The most starred package, already reached twice.
			"a/popular": {RepoOwner: "a", RepoName: "popular", Stars: 100, Round: 2},
			// Reached once, and less popular.
			"b/seen": {RepoOwner: "b", RepoName: "seen", Stars: 50, Round: 1},
			// Never reached, and least popular of the three.
			"c/waiting": {RepoOwner: "c", RepoName: "waiting", Stars: 1},
			// Never reached either, so stars decide between the two of them.
			"d/waiting2": {RepoOwner: "d", RepoName: "waiting2", Stars: 2},
		},
	}
	got := make([]string, 0, len(s.Packages))
	for _, c := range order(s) {
		got = append(got, c.Name)
	}
	want := []string{"d/waiting2", "c/waiting", "b/seen", "a/popular"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("order is wrong (-want +got):\n%s", diff)
	}
}

// TestLimitBudget checks that a package g2 holds little of takes only enough of the
// run to reach breadthDepth. Without the cap a run spends itself on the most starred
// package while nothing else gets a version at all.
func TestLimitBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		budget   int
		existing int
		want     int
	}{
		{name: "nothing generated yet", budget: 300, existing: 0, want: breadthDepth},
		{name: "partly generated", budget: 300, existing: 3, want: breadthDepth - 3},
		{name: "past the breadth depth", budget: 300, existing: breadthDepth, want: 300},
		{
			// The run has less left than the package would take, so the run's budget
			// is what bounds it.
			name:   "the run is nearly spent",
			budget: 2, existing: 0, want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			existing := make(map[string]struct{}, tt.existing)
			for i := range tt.existing {
				existing[string(rune('a'+i))] = struct{}{}
			}
			if got := limitBudget(tt.budget, existing); got != tt.want {
				t.Errorf("limitBudget is %d, want %d", got, tt.want)
			}
		})
	}
}
