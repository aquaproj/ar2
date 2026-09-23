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
	gogithub "github.com/google/go-github/v92/github"
)

type fakeRegistry struct {
	committed []*g2.File
	created   int
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
func (f *fakeRegistry) Config(_ context.Context, _ string) (*aquag2.Config, error) {
	return nil, nil //nolint:nilnil
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

// These tests reach the work a package needs, not the sweep that decides which
// packages need any.
func (failingAutoMerger) Versions(_ context.Context, _ []github.Repo) (map[string][]string, map[string]string, error) {
	return nil, nil, nil
}

func (failingAutoMerger) Tags(_ context.Context, _ []github.Repo) (map[string][]string, map[string]string, error) {
	return nil, nil, nil
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

// fakeIndex records what was added to the catalogue.
type fakeIndex struct {
	added map[string]*aquag2.Config
	err   error
}

func (f *fakeIndex) AddPackage(_ context.Context, _ *slog.Logger, pkgName string, cfg *aquag2.Config) error {
	if f.added == nil {
		f.added = map[string]*aquag2.Config{}
	}
	f.added[pkgName] = cfg
	return f.err
}

// A package whose branch already holds its definition has been in the catalogue
// since the run that brought it, so a pull request adding versions leaves it alone.
func TestOpenPullRequest_indexUntouched(t *testing.T) {
	t.Parallel()
	idx := &fakeIndex{}
	c := New(ghClient(t), nil, &fakeRegistry{}, failingAutoMerger{}, nil, idx)
	if err := c.openPullRequest(t.Context(), discardLogger(), &definition{config: &aquag2.Config{}, fromBranch: true}, "cli/cli", []*version{
		{Version: "v2.1.0", Registry: &generate.Registry{}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(idx.added) != 0 {
		t.Errorf("added %v to the catalogue, want nothing", idx.added)
	}
}

// The definition the pull request carries is what the catalogue entry is made of.
func TestAddToIndex(t *testing.T) {
	t.Parallel()
	idx := &fakeIndex{}
	cfg := &aquag2.Config{}
	New(ghClient(t), nil, nil, nil, nil, idx).addToIndex(t.Context(), discardLogger(), "cli/cli", cfg)
	if idx.added["cli/cli"] != cfg {
		t.Errorf("the catalogue got %v, want the definition just written", idx.added)
	}
}

// The catalogue is a separate pull request against a separate branch, and the
// reconciliation adds whatever was missed. Failing the package over it would throw
// away the versions that were generated.
func TestAddToIndex_errorIsNotFatal(t *testing.T) {
	t.Parallel()
	idx := &fakeIndex{err: errors.New("the catalogue is unreachable")}
	New(ghClient(t), nil, nil, nil, nil, idx).addToIndex(t.Context(), discardLogger(), "cli/cli", &aquag2.Config{})
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
