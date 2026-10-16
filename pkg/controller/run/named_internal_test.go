package run

import (
	"testing"
	"time"

	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/state"
)

// A package with nothing new is passed over, unless the run was asked for it by name: then
// its history is walked, which is what reaches the versions older than the sweep reads.
func TestTodo_named(t *testing.T) {
	t.Parallel()
	now := time.Now()
	swept := map[string]*github.Found{"anomalyco/opencode": {Versions: []string{"v1.19.1"}, Stable: true}}
	newCandidate := func() *Candidate {
		return &Candidate{Name: "anomalyco/opencode", Package: &state.Package{
			RepoOwner: "anomalyco", RepoName: "opencode",
			Versions: state.Fingerprint([]string{"v1.19.1"}), CaughtUp: true, LastDeepCheck: now,
		}}
	}
	c := New(ghClient(t), nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
	inFlight := map[string]struct{}{}
	if todo, _ := c.todo(discardLogger(), newCandidate(), inFlight, nil, swept, now, false); todo != workNone {
		t.Errorf("got %v for a package with nothing new, want nothing", todo)
	}
	if todo, _ := c.todo(discardLogger(), newCandidate(), inFlight, nil, swept, now, true); todo != workDeep {
		t.Errorf("got %v for a package asked for by name, want its history walked", todo)
	}
}
