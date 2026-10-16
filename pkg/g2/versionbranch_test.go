package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
)

// A branch per version, so that a version that can't merge doesn't sit in the same pull
// request as the ones that can.
func TestVersionHeadBranchName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id, version, want string
	}{
		{id: "1790772767", version: "v0.8.0", want: "ar2_1790772767_v0.8.0"},
		// A version holds a slash of its own, which is escaped the way a name is.
		{id: "1790772903", version: "kustomize/v5.8.1", want: "ar2_1790772903_kustomize_2fv5.8.1"},
		{id: "1790772768", version: "v2.0.0+build.1", want: "ar2_1790772768_v2.0.0_2bbuild.1"},
	}
	for _, tt := range tests {
		t.Run(tt.id+"@"+tt.version, func(t *testing.T) {
			t.Parallel()
			if got := g2.VersionHeadBranchName(tt.id, tt.version); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The branch of the versions waiting for a definition names the package, and no version's
// branch can be it.
func TestWaitingHeadBranchName(t *testing.T) {
	t.Parallel()
	branch := g2.WaitingHeadBranchName("1790772767")
	if branch != "ar2_1790772767__needs-definition" {
		t.Errorf("got %q", branch)
	}
	if g2.IsVersionHeadBranch("1790772767", branch) {
		t.Error("the waiting branch reads as a version's")
	}
	if id, ok := g2.HeadBranchID(branch); !ok || id != "1790772767" {
		t.Errorf("the waiting branch is %q's", id)
	}
	if branch == g2.VersionHeadBranchName("1790772767", "_needs-definition") {
		t.Error("a version's branch can be the waiting one")
	}
}

// The branch of the versions left for review names the package, and no version's branch can
// be it.
func TestReviewHeadBranchName(t *testing.T) {
	t.Parallel()
	branch := g2.ReviewHeadBranchName("1790772767")
	if branch != "ar2_1790772767__needs-review" {
		t.Errorf("got %q", branch)
	}
	if g2.IsVersionHeadBranch("1790772767", branch) {
		t.Error("the review branch reads as a version's")
	}
	if id, ok := g2.HeadBranchID(branch); !ok || id != "1790772767" {
		t.Errorf("the review branch is %q's", id)
	}
	if branch == g2.VersionHeadBranchName("1790772767", "_needs-review") {
		t.Error("a version's branch can be the review one")
	}
}
