package run

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

type fakeRegistry struct {
	committed []*g2.File
	created   int
	// fromBranch is the branch the last pull request was opened from, which for a version
	// waiting for a definition is one of its own.
	fromBranch string
	// commitBranch is where the last commit went.
	commitBranch string
	// labels are what was put on the pull requests.
	labels []string
}

func (f *fakeRegistry) Versions(_ context.Context, _ *slog.Logger, _ string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (f *fakeRegistry) PackagesInFlight(_ context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (f *fakeRegistry) EnsurePackageBranch(_ context.Context, _ string) (string, error) {
	return "base-sha", nil
}

// Version is never asked for: these tests generate for a package the repository
// holds nothing of, so there is no earlier version to compare the signing against.
func (f *fakeRegistry) Version(_ context.Context, _, _ string) (*aquag2.Registry, error) {
	return nil, nil //nolint:nilnil
}

// Config is never asked for: these tests pass the definition in, so that they see
// only the generated files.
func (f *fakeRegistry) RegistryConfig(_ context.Context, _ string) (*g2.RegistryConfig, error) {
	return &g2.RegistryConfig{}, nil
}

func (f *fakeRegistry) Config(_ context.Context, _ string) (*aquag2.Config, error) {
	return nil, nil //nolint:nilnil
}

// File is never asked for: nothing here regenerates a version the branch holds.
func (f *fakeRegistry) File(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (f *fakeRegistry) Commit(_ context.Context, branch, _, _ string, files []*g2.File) error {
	f.commitBranch = branch
	f.committed = files
	return nil
}

// The readers of a branch that isn't a package's own are never asked here: nothing in these
// tests regenerates what is waiting for a definition.
func (f *fakeRegistry) VersionsOnRef(_ context.Context, _ *slog.Logger, _ string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (f *fakeRegistry) ConfigOnRef(_ context.Context, _ string) (*aquag2.Config, error) {
	return nil, nil //nolint:nilnil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, _ string) (string, error) {
	return "base-sha", nil
}

func (f *fakeRegistry) WaitingPullRequest(_ context.Context, _ string) (*gogithub.PullRequest, error) {
	return nil, nil //nolint:nilnil
}

func (f *fakeRegistry) Label(_ context.Context, _ *slog.Logger, _ int, name string) {
	f.labels = append(f.labels, name)
}

// CreatePullRequestFrom is what a version waiting for a definition is opened with.
func (f *fakeRegistry) CreatePullRequestFrom(_ context.Context, _ *slog.Logger, head, _, _, _ string) (*gogithub.PullRequest, error) {
	f.fromBranch = head
	f.created++
	return &gogithub.PullRequest{Number: new(2), NodeID: new("node")}, nil
}

func (f *fakeRegistry) CreatePullRequest(_ context.Context, _ *slog.Logger, _, _, _ string) (*gogithub.PullRequest, error) {
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

// These tests reach the work a package needs, not the sweep that decides which
// packages need any.
func (failingAutoMerger) Versions(_ context.Context, _ []github.Repo) (*github.Sweep, error) {
	return &github.Sweep{}, nil
}

func (failingAutoMerger) Tags(_ context.Context, _ []github.Repo) (*github.Sweep, error) {
	return &github.Sweep{}, nil
}

// ghClient is a client that is never called: these tests don't reach anything that
// makes a request. It is a real one because the controller builds what it needs out
// of it when it is created.
func ghClient(t *testing.T) *gogithub.Client {
	t.Helper()
	gh, err := gogithub.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	return gh
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
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
	err := c.openPullRequest(t.Context(), discardLogger(), &definition{config: &aquag2.Config{}, fromBranch: true}, "cli/cli", []*version{
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
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
	if err := c.openPullRequest(t.Context(), discardLogger(), &definition{config: &aquag2.Config{}, fromBranch: true}, "cli/cli", []*version{
		{Version: "v2.1.0", Registry: &generate.Registry{}},
		{Version: "v2.2.0", Registry: &generate.Registry{}},
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"versions/v2.1.0/registry-1.json", "versions/v2.2.0/registry-1.json"}
	if len(reg.committed) != len(want) {
		t.Fatalf("%d files were committed, want %d", len(reg.committed), len(want))
	}
	for i, file := range reg.committed {
		if file.Path != want[i] {
			t.Errorf("file %d is at %q, want %q", i, file.Path, want[i])
		}
	}
}

func TestPRBodyReason(t *testing.T) {
	t.Parallel()
	body := prBody([]*version{{Version: "v1.18.32"}}, []string{`Version in ["v0.1.84", "v0.1.92"]`}, true)
	for _, want := range []string{"v1.18.32", "couldn't be turned into boundaries", `Version in ["v0.1.84", "v0.1.92"]`, "Auto-merge is off. What it is waiting on is above."} {
		if !strings.Contains(body, want) {
			t.Fatalf("the body doesn't mention %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "run's log") {
		t.Fatalf("the body sends the reader to the log although it says the reason:\n%s", body)
	}
}

// The versions that can't merge go together, on a branch named after the oldest, and the ones
// that can are left for the pull request of the package.
func TestOpenUnresolved(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
	// Newest first, the way a run holds them.
	versions := []*version{
		{Version: "v0.9.0", Registry: &generate.Registry{}, Unresolved: []string{"darwin/arm64: exa"}},
		{Version: "v0.8.0", Registry: &generate.Registry{}, Unresolved: []string{"darwin/arm64: exa"}},
	}
	if err := c.openUnresolved(t.Context(), discardLogger(), "ogham/exa", versions, nil); err != nil {
		t.Fatal(err)
	}
	if want := "ar2_ogham_2fexa_v0.8.0"; reg.commitBranch != want {
		t.Errorf("committed to %q, want the branch of the oldest of them, %q", reg.commitBranch, want)
	}
	if reg.fromBranch != reg.commitBranch {
		t.Errorf("opened from %q, want %q", reg.fromBranch, reg.commitBranch)
	}
	if len(reg.committed) != 2 {
		t.Fatalf("committed %d files, want both versions", len(reg.committed))
	}
	if reg.created != 1 {
		t.Errorf("opened %d pull requests, want one for the two of them", reg.created)
	}
	if diff := cmp.Diff([]string{g2.NeedsDefinitionLabel}, reg.labels); diff != "" {
		t.Errorf("the labels are wrong (-want +got):\n%s", diff)
	}
}

// A package whose versions are already waiting is left alone: committing would reset the
// branch, and what is on it may be the definition somebody is in the middle of writing.
func TestOpenUnresolved_alreadyWaiting(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
	inFlight := map[string]struct{}{"ar2_ogham_2fexa_v0.7.0": {}}
	versions := []*version{
		{Version: "v0.8.0", Registry: &generate.Registry{}, Unresolved: []string{"darwin/arm64: exa"}},
	}
	if err := c.openUnresolved(t.Context(), discardLogger(), "ogham/exa", versions, inFlight); err != nil {
		t.Fatal(err)
	}
	if reg.created != 0 || reg.committed != nil {
		t.Errorf("opened %d pull requests and committed %+v, want neither", reg.created, reg.committed)
	}
}

// The pull request of the package itself is not one of these, so it doesn't stop them.
func TestWaiting(t *testing.T) {
	t.Parallel()
	own := map[string]struct{}{"ar2_ogham_2fexa": {}}
	if branch, ok := waiting("ogham/exa", own); ok {
		t.Errorf("the package's own branch counted as one waiting: %q", branch)
	}
	version := map[string]struct{}{"ar2_ogham_2fexa_v0.7.0": {}}
	if _, ok := waiting("ogham/exa", version); !ok {
		t.Error("a version's branch should count as one waiting")
	}
	other := map[string]struct{}{"ar2_cli_2fcli_v2.0.0": {}}
	if _, ok := waiting("ogham/exa", other); ok {
		t.Error("another package's branch counted as this one's")
	}
}

// What a run does with a mixture: the ones that can merge and the ones that can't are told
// apart by whether anything went unresolved.
func TestPartition(t *testing.T) {
	t.Parallel()
	sound, unresolved := partition([]*version{
		{Version: "v0.10.0"},
		{Version: "v0.9.0", Unresolved: []string{"darwin/arm64: exa"}},
		{Version: "v0.8.0", Unresolved: []string{"darwin/arm64: exa"}},
	})
	if len(sound) != 1 || sound[0].Version != "v0.10.0" {
		t.Errorf("the sound ones are %+v", sound)
	}
	if len(unresolved) != 2 || unresolved[1].Version != "v0.8.0" {
		t.Errorf("the ones waiting are %+v", unresolved)
	}
}
