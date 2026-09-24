package run

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
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

// TestLimitBudget checks how much of a run one package may take: enough of a turn to
// reach the breadth, and no more, so that a run isn't spent on the most starred
// package while nothing else gets a version at all.
func TestLimitBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		budget   int
		existing int
		want     *share
	}{
		{name: "nothing generated yet", budget: 300, existing: 0, want: &share{attempts: 5, versions: 5}},
		{name: "partly generated", budget: 300, existing: 3, want: &share{attempts: 2, versions: 2}},
		{
			// Past the breadth there is nothing left to reach, so the package takes
			// what the run has left and the versions bound doesn't apply.
			name: "past the breadth", budget: 300, existing: 5, want: &share{attempts: 300},
		},
		{name: "the run is nearly spent", budget: 2, existing: 0, want: &share{attempts: 2, versions: 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkShare(t, tt.budget, tt.existing, nil, tt.want)
		})
	}
}

// TestLimitBudget_configured checks the two bounds a registry can set. They are
// separate because the versions likeliest to fail are the newest, which are the ones
// a turn starts from: a package would otherwise stop short of the breadth every turn.
func TestLimitBudget_configured(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing int
		breadth  *g2.Breadth
		want     *share
	}{
		{
			// What the registry asks for: try twenty and stop at ten.
			name: "try twenty, stop at ten", existing: 0,
			breadth: &g2.Breadth{Versions: 10, Attempts: 20},
			want:    &share{attempts: 20, versions: 10},
		},
		{
			// One that has some already needs fewer to reach ten, but may still try
			// the whole twenty to get there.
			name: "partly generated", existing: 8,
			breadth: &g2.Breadth{Versions: 10, Attempts: 20},
			want:    &share{attempts: 20, versions: 2},
		},
		{
			// Without an attempts of its own, a turn tries as many as it wants and
			// no more, so a version that fails costs the package a lap.
			name: "only the versions are set", existing: 0,
			breadth: &g2.Breadth{Versions: 10},
			want:    &share{attempts: 10, versions: 10},
		},
		{
			name: "only the versions are set, partly generated", existing: 8,
			breadth: &g2.Breadth{Versions: 10},
			want:    &share{attempts: 2, versions: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkShare(t, 300, tt.existing, tt.breadth, tt.want)
		})
	}
}

// checkShare runs limitBudget against a package that already has the given number of
// versions, whatever they are called.
func checkShare(t *testing.T, budget, existing int, breadth *g2.Breadth, want *share) {
	t.Helper()
	held := make(map[string]struct{}, existing)
	for i := range existing {
		held[string(rune('a'+i))] = struct{}{}
	}
	if diff := cmp.Diff(want, limitBudget(budget, held, breadth), cmp.AllowUnexported(share{})); diff != "" {
		t.Errorf("the share is wrong (-want +got):\n%s", diff)
	}
}

// TestRejoin checks that a package which was out of the order comes back at the end
// of it rather than at the front.
//
// A run gives a turn to the packages with the fewest, so the counts of the packages
// in the order are never more than one apart. A package further behind than that
// wasn't in the order to be counted -- it was on the registry's ignored list -- and
// left as it was it would be first in every run until it caught up.
func TestRejoin(t *testing.T) {
	t.Parallel()
	s := &state.State{
		Packages: map[string]*state.Package{
			"a/left-out": {RepoOwner: "a", RepoName: "left-out", Stars: 1, Round: 2},
			"b/waiting":  {RepoOwner: "b", RepoName: "waiting", Stars: 10, Round: 8},
			"c/popular":  {RepoOwner: "c", RepoName: "popular", Stars: 99, Round: 9},
			"d/seen":     {RepoOwner: "d", RepoName: "seen", Stars: 50, Round: 9},
		},
	}
	got := make([]string, 0, len(s.Packages))
	for _, candidate := range rejoin(discardLogger(), order(s)) {
		got = append(got, candidate.Name)
	}
	// The one that was left out is now at the back with the rest, where its stars
	// decide where it sits among them, rather than ahead of the whole registry.
	want := []string{"b/waiting", "c/popular", "d/seen", "a/left-out"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("order is wrong (-want +got):\n%s", diff)
	}
	if s.Packages["a/left-out"].Round != 9 {
		t.Errorf("it rejoined at %d, want 9", s.Packages["a/left-out"].Round)
	}
	// A package that is merely next in line is left alone: being one behind is what
	// waiting for a turn looks like.
	if s.Packages["b/waiting"].Round != 8 {
		t.Errorf("the package waiting its turn moved to %d, want 8", s.Packages["b/waiting"].Round)
	}
}
