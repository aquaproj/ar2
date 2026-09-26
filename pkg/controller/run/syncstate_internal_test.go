package run

import (
	"context"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/google/go-cmp/cmp"
)

// starFetcher answers with fixed star counts, and refuses one repository the way an
// organization's IP allow list does.
type starFetcher struct {
	stars map[string]int
}

func (starFetcher) EnableAutoMerge(_ context.Context, _ string) error { return nil }

func (f starFetcher) GetStars(_ context.Context, repos []github.Repo) (map[string]int, map[string]string, error) {
	stars := map[string]int{}
	reasons := map[string]string{}
	for _, repo := range repos {
		if star, ok := f.stars[repo.String()]; ok {
			stars[repo.String()] = star
			continue
		}
		reasons[repo.String()] = "NOT_FOUND"
	}
	return stars, reasons, nil
}

func (starFetcher) FillForbiddenStars(_ context.Context, _ map[string]int, _ map[string]string) {}

// TestSyncState checks that a package aqua-registry has gained is added with its
// star count, and that the packages already known are left alone.
//
// Without this a package added after 'ar2 init' is never ordered, so it is never
// processed: it waits for the next init rather than for the next run.
func TestSyncState(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{
		"cli/cli": {RepoOwner: "cli", RepoName: "cli", Stars: 100},
	}}
	pkgInfos := map[string]*aquaregistry.PackageInfo{
		"cli/cli":       {RepoOwner: "cli", RepoName: "cli"},
		"aquaproj/aqua": {RepoOwner: "aquaproj", RepoName: "aqua"},
		"gone/gone":     {RepoOwner: "gone", RepoName: "gone"},
	}
	c := New(ghClient(t), nil, nil, starFetcher{stars: map[string]int{"aquaproj/aqua": 42}}, nil)

	changed, err := c.SyncState(t.Context(), discardLogger(), s, pkgInfos)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("adding packages should report the state as changed")
	}
	if got := s.Packages["aquaproj/aqua"]; got == nil || got.Stars != 42 {
		t.Errorf("the new package should carry its star count, got %+v", got)
	}
	// A package whose star count can't be read is still added: being processed last
	// is better than not being processed.
	if _, ok := s.Packages["gone/gone"]; !ok {
		t.Error("a package without a star count should still be added")
	}
	// An existing count is left alone; refreshing every one of them is what
	// 'ar2 init' is for.
	if s.Packages["cli/cli"].Stars != 100 {
		t.Errorf("an existing star count should not change, got %d", s.Packages["cli/cli"].Stars)
	}
}

// TestSyncState_nothingNew checks that a run with nothing to add doesn't report the
// state as changed, which would push it to the container registry for no reason.
func TestSyncState_nothingNew(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{
		"cli/cli": {RepoOwner: "cli", RepoName: "cli", Stars: 100},
	}}
	c := New(ghClient(t), nil, nil, starFetcher{}, nil)
	changed, err := c.SyncState(t.Context(), discardLogger(), s, map[string]*aquaregistry.PackageInfo{
		"cli/cli": {RepoOwner: "cli", RepoName: "cli"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("nothing was added, so the state didn't change")
	}
}

// The star fetcher's tests are about the state, not the sweep.
func (starFetcher) Versions(_ context.Context, _ []github.Repo) (*github.Sweep, error) {
	return &github.Sweep{}, nil
}

func (starFetcher) Tags(_ context.Context, _ []github.Repo) (*github.Sweep, error) {
	return &github.Sweep{}, nil
}

// TestSyncState_joinsTheCurrentLap checks that a package the registry has just gained
// starts with the fewest turns anything in the order has: it waits for this lap's
// turn rather than the next one.
//
// Starting from none would put it ahead of every package already known and keep it
// there until it caught up -- a turn in every run for as long as that took, which is
// what counting turns exists to stop. Starting from the most would cost it a lap for
// no reason but having arrived late, and it is the package with nothing generated at
// all.
func TestSyncState_joinsTheCurrentLap(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{
		"cli/cli":            {RepoOwner: "cli", RepoName: "cli", Stars: 100, Round: 7},
		"suzuki-shunsuke/ci": {RepoOwner: "suzuki-shunsuke", RepoName: "ci", Stars: 1, Round: 6},
	}}
	pkgInfos := map[string]*aquaregistry.PackageInfo{
		"cli/cli":      {RepoOwner: "cli", RepoName: "cli"},
		"junegunn/fzf": {RepoOwner: "junegunn", RepoName: "fzf"},
	}
	c := New(ghClient(t), nil, nil, starFetcher{stars: map[string]int{"junegunn/fzf": 50}}, nil)

	if _, err := c.SyncState(t.Context(), discardLogger(), s, pkgInfos); err != nil {
		t.Fatal(err)
	}
	added, ok := s.Packages["junegunn/fzf"]
	if !ok {
		t.Fatal("the new package wasn't added")
	}
	if added.Round != 6 {
		t.Errorf("the new package joined at %d, want 6, the turns of those still waiting", added.Round)
	}
	// It takes its place among the packages waiting for this lap's turn, where its
	// stars decide where it sits, rather than ahead of the whole registry or behind
	// all of it.
	got := make([]string, 0, len(s.Packages))
	for _, candidate := range order(s) {
		got = append(got, candidate.Name)
	}
	want := []string{"junegunn/fzf", "suzuki-shunsuke/ci", "cli/cli"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("order is wrong (-want +got):\n%s", diff)
	}
}
