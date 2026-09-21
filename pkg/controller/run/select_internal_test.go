package run

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/state"
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
