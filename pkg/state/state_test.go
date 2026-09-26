package state_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/state"
)

// A renamed package keeps what is known about it, including its place in the order: it is
// the same package and has had the same turns.
func TestState_Rename(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{
		"sst/opencode": {RepoOwner: "sst", RepoName: "opencode", Stars: 200000, Round: 7},
	}}
	s.Rename("sst/opencode", "anomalyco/opencode")

	if _, ok := s.Packages["sst/opencode"]; ok {
		t.Error("the old name is still held")
	}
	pkg, ok := s.Packages["anomalyco/opencode"]
	if !ok {
		t.Fatal("the new name isn't held")
	}
	if pkg.RepoOwner != "anomalyco" || pkg.RepoName != "opencode" {
		t.Errorf("the repository is %s/%s", pkg.RepoOwner, pkg.RepoName)
	}
	if pkg.Stars != 200000 || pkg.Round != 7 {
		t.Errorf("got %d stars and %d turns, want what it had", pkg.Stars, pkg.Round)
	}
	if s.Renamed["sst/opencode"] != "anomalyco/opencode" {
		t.Errorf("the old name maps to %q", s.Renamed["sst/opencode"])
	}
}

// A package renamed twice has every name it has had pointing at the newest one, so the
// oldest doesn't point at a name the registry has stopped holding.
func TestState_Rename_twice(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{
		"a/a": {RepoOwner: "a", RepoName: "a"},
	}}
	s.Rename("a/a", "b/b")
	s.Rename("b/b", "c/c")

	if _, ok := s.Packages["c/c"]; !ok {
		t.Fatal("the newest name isn't held")
	}
	for _, old := range []string{"a/a", "b/b"} {
		if got := s.Renamed[old]; got != "c/c" {
			t.Errorf("%s maps to %q, want the newest name", old, got)
		}
	}
}

// A package the state doesn't hold has nothing to move, and recording the rename anyway
// would keep a name out of the state that nothing had put there.
func TestState_Rename_unknown(t *testing.T) {
	t.Parallel()
	s := &state.State{Packages: map[string]*state.Package{}}
	s.Rename("a/a", "b/b")
	if len(s.Packages) != 0 || len(s.Renamed) != 0 {
		t.Errorf("got %+v and %+v", s.Packages, s.Renamed)
	}
}
