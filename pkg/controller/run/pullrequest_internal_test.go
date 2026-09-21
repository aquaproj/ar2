package run

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	gogithub "github.com/google/go-github/v92/github"
	"github.com/szksh-lab-2/ar2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/github"
)

type fakeRegistry struct {
	committed []*g2.File
	created   int
}

func (f *fakeRegistry) Versions(_ context.Context, _ string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (f *fakeRegistry) PackagesInFlight(_ context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (f *fakeRegistry) EnsurePackageBranch(_ context.Context, _ string) (string, error) {
	return "base-sha", nil
}

func (f *fakeRegistry) Commit(_ context.Context, _, _, _ string, files []*g2.File) error {
	f.committed = files
	return nil
}

func (f *fakeRegistry) CreatePullRequest(_ context.Context, _, _, _ string) (*gogithub.PullRequest, error) {
	f.created++
	return &gogithub.PullRequest{Number: new(1), NodeID: new("node")}, nil
}

// failingAutoMerger stands in for GitHub refusing auto-merge, which it does on a
// branch with no required checks.
type failingAutoMerger struct{}

func (failingAutoMerger) EnableAutoMerge(_ context.Context, _ string) error {
	return errors.New("auto-merge is not allowed for this repository")
}

func (failingAutoMerger) GetStars(_ context.Context, _ []github.Repo) (map[string]int, map[string]string, error) {
	return map[string]int{}, map[string]string{}, nil
}

func (failingAutoMerger) FillForbiddenStars(_ context.Context, _ map[string]int, _ map[string]string) {
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestOpenPullRequest_autoMergeFails checks that a pull request which couldn't be set
// to auto-merge still counts as done.
//
// It didn't: enabling auto-merge returned an error, the package was reported as
// having generated nothing, and the run's budget never went down. Every remaining
// package then got a pull request of its own, so asking for two versions produced
// one pull request per package instead.
func TestOpenPullRequest_autoMergeFails(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(nil, nil, reg, failingAutoMerger{}, nil)
	err := c.openPullRequest(t.Context(), discardLogger(), "cli/cli", []*version{
		{Version: "v2.1.0", Registry: &generate.Registry{}},
	})
	if err != nil {
		t.Fatalf("a pull request that can't auto-merge is still a pull request: %v", err)
	}
	if reg.created != 1 {
		t.Errorf("one pull request should have been created, got %d", reg.created)
	}
}

// TestOpenPullRequest_paths checks that each version lands where the package branch
// keeps it.
func TestOpenPullRequest_paths(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(nil, nil, reg, failingAutoMerger{}, nil)
	if err := c.openPullRequest(t.Context(), discardLogger(), "cli/cli", []*version{
		{Version: "v2.1.0", Registry: &generate.Registry{}},
		{Version: "v2.2.0", Registry: &generate.Registry{}},
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"versions/v2.1.0/registry.json", "versions/v2.2.0/registry.json"}
	if len(reg.committed) != len(want) {
		t.Fatalf("%d files were committed, want %d", len(reg.committed), len(want))
	}
	for i, file := range reg.committed {
		if file.Path != want[i] {
			t.Errorf("file %d is at %q, want %q", i, file.Path, want[i])
		}
	}
}
