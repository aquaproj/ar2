package run

import (
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
)

// The versions left for review go to one pull request of the package's, and a version that
// needs nothing still gets one of its own.
func TestOpenPullRequests_review(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	generated := []*version{
		{Version: "v1.18.6", Registry: &generate.Registry{}, NeedsReview: true},
		{Version: "v1.18.5", Registry: &generate.Registry{}},
		{Version: "v1.18.4", Registry: &generate.Registry{}, NeedsReview: true},
	}
	def := &definition{config: &aquag2.Config{}, held: true}
	if err := c.openPullRequests(t.Context(), discardLogger(), def, "anomalyco/opencode", generated, nil, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if reg.created != 2 {
		t.Errorf("opened %d pull requests, want one for the version that needs nothing and one for the two left for review", reg.created)
	}
	if want := g2.ReviewHeadBranchName(fakeID); reg.fromBranch != want {
		t.Errorf("the last pull request was opened from %q, want %q", reg.fromBranch, want)
	}
	if len(reg.committed) != 2 {
		t.Errorf("committed %d files to the review branch, want both versions left for review", len(reg.committed))
	}
}

// A pull request of the versions left for review that is open already gets the new ones on
// top of its branch, rather than a second pull request.
func TestOpenReview_open(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	branch := g2.ReviewHeadBranchName(fakeID)
	versions := []*version{{Version: "v1.18.7", Registry: &generate.Registry{}, NeedsReview: true}}
	if err := c.openReview(t.Context(), discardLogger(), &definition{config: &aquag2.Config{}, held: true}, "anomalyco/opencode",
		versions, map[string]struct{}{branch: {}}); err != nil {
		t.Fatal(err)
	}
	if reg.created != 0 {
		t.Errorf("opened %d pull requests, want none", reg.created)
	}
	if reg.commitBranch != branch {
		t.Errorf("committed to %q, want %q", reg.commitBranch, branch)
	}
}

// A package the registry doesn't hold yet isn't gathered: its first version's pull request
// carries the definition.
func TestOpenPullRequests_reviewNewPackage(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	generated := []*version{{Version: "v1.0.0", Registry: &generate.Registry{}, NeedsReview: true}}
	if err := c.openPullRequests(t.Context(), discardLogger(), &definition{config: &aquag2.Config{}}, "foo/foo", generated, nil, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if want := g2.VersionHeadBranchName(fakeID, "v1.0.0"); reg.fromBranch != want {
		t.Errorf("opened from %q, want the version's own branch %q", reg.fromBranch, want)
	}
}
