package run

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// regenerateRegistry is a registry that answers what a regeneration asks before it
// generates anything: what is in flight, what definition the branch holds, and which
// versions it has.
type regenerateRegistry struct {
	fakeRegistry
	inFlight map[string]struct{}
	config   *aquag2.Config
	versions map[string]struct{}
}

func (f *regenerateRegistry) PackagesInFlight(_ context.Context) (map[string]struct{}, error) {
	return f.inFlight, nil
}

func (f *regenerateRegistry) Config(_ context.Context, _ string) (*aquag2.Config, error) {
	return f.config, nil
}

// ConfigOnRef and VersionsOnRef are what a regeneration reads, whichever branch it is working
// on: the package's own for what the registry holds.
func (f *regenerateRegistry) ConfigOnRef(_ context.Context, _ string) (*aquag2.Config, error) {
	return f.config, nil
}

func (f *regenerateRegistry) VersionsOnRef(_ context.Context, _ *slog.Logger, _ string) (map[string]struct{}, error) {
	return f.versions, nil
}

func (f *regenerateRegistry) Versions(_ context.Context, _ *slog.Logger, _ string) (map[string]struct{}, error) {
	return f.versions, nil
}

// TestRegenerate_pullRequestInFlight checks that a package with an open pull request
// is refused.
//
// The commit is written against the package branch and the head branch is pointed at
// it, so going ahead would discard the commits of a pull request somebody may be in
// the middle of reading.
func TestRegenerate_pullRequestInFlight(t *testing.T) {
	t.Parallel()
	reg := &regenerateRegistry{
		inFlight: map[string]struct{}{g2.HeadBranchName(fakeID): {}},
		config:   &aquag2.Config{},
	}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	_, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{PkgName: "cli/cli"})
	if !errors.Is(err, errPullRequestInFlight) {
		t.Fatalf("a package with an open pull request should be refused, got %v", err)
	}
	if reg.created != 0 {
		t.Errorf("nothing should have been created, got %d", reg.created)
	}
}

// TestRegenerate_noDefinition checks that a package whose branch holds no definition
// is refused rather than generated from aqua-registry's.
//
// Converting it here would produce files the registry's own definition doesn't
// produce, which is the thing this command exists to correct.
func TestRegenerate_noDefinition(t *testing.T) {
	t.Parallel()
	reg := &regenerateRegistry{inFlight: map[string]struct{}{}}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	_, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{PkgName: "cli/cli"})
	if !errors.Is(err, errNoDefinition) {
		t.Fatalf("a package without a definition on its branch should be refused, got %v", err)
	}
}

// TestRegenerate_versionNotHeld checks that a version the registry doesn't hold is
// refused instead of added. Adding one is a run's job.
func TestRegenerate_versionNotHeld(t *testing.T) {
	t.Parallel()
	reg := &regenerateRegistry{
		inFlight: map[string]struct{}{},
		config:   &aquag2.Config{},
		versions: map[string]struct{}{"v2.1.0": {}},
	}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	_, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{
		PkgName:  "cli/cli",
		Versions: []string{"v2.2.0"},
	})
	if !errors.Is(err, errVersionNotHeld) {
		t.Fatalf("a version the registry doesn't hold should be refused, got %v", err)
	}
}

// TestRegenerate_nothingChanged checks that a package whose versions come out the
// same comes to nothing.
//
// This is what makes it safe to point the command at a whole package: the answer to
// "did anything move" is no pull request rather than one that changes nothing.
func TestRegenerate_nothingChanged(t *testing.T) {
	t.Parallel()
	reg := &regenerateRegistry{
		inFlight: map[string]struct{}{},
		config:   &aquag2.Config{},
		versions: map[string]struct{}{},
	}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	changed, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{PkgName: "cli/cli"})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Errorf("nothing held is nothing to change, got %d", changed)
	}
	if reg.created != 0 {
		t.Errorf("no pull request should have been created, got %d", reg.created)
	}
}

// TestAllVersions checks that the versions the registry holds come out in a stable
// order, newest-looking first.
func TestAllVersions(t *testing.T) {
	t.Parallel()
	got := allVersions(map[string]struct{}{"v2.1.0": {}, "v2.10.0": {}, "v2.2.0": {}})
	want := []string{"v2.2.0", "v2.10.0", "v2.1.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, version := range want {
		if got[i] != version {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestRegenerateTitle checks what the pull request is called.
func TestRegenerateTitle(t *testing.T) {
	t.Parallel()
	versions := []*regenerated{
		{version: &version{Version: "v2.1.0"}},
		{version: &version{Version: "v2.2.0"}},
	}
	if got, want := regenerateTitle("cli/cli", versions[:1]), "fix(cli/cli): generate v2.1.0 again"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := regenerateTitle("cli/cli", versions), "fix(cli/cli): generate 2 versions again"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// waitingRegistry is a package with versions waiting for a definition: a pull request whose
// branch is named after one of them, holding the versions and the definition somebody wrote.
type waitingRegistry struct {
	regenerateRegistry
	pr *gogithub.PullRequest
}

func (f *waitingRegistry) WaitingPullRequest(_ context.Context, _ string) (*gogithub.PullRequest, error) {
	return f.pr, nil
}

// --pending reads and writes the pull request's branch, not the package's. What is on it is the
// definition that makes the versions true, and a commit built on the package branch would
// write that away.
func TestRegenerate_pendingWorksOnThatBranch(t *testing.T) {
	t.Parallel()
	branch := "ar2_1790772767_v0.8.0"
	held := regenerateRegistry{
		inFlight: map[string]struct{}{branch: {}},
		config:   &aquag2.Config{},
		versions: map[string]struct{}{},
	}
	reg := &waitingRegistry{regenerateRegistry: held, pr: &gogithub.PullRequest{
		Number: new(9),
		Head:   &gogithub.PullRequestBranch{Ref: new(branch)},
	}}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	changed, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{
		PkgName: "ogham/exa",
		Pending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Errorf("regenerated %d versions, want none: the branch holds none", changed)
	}
	// An open pull request of the package's own doesn't stop it, which is the check the
	// other mode makes.
	if reg.created != 0 {
		t.Errorf("opened %d pull requests, want none: the one that was waiting is the answer", reg.created)
	}
}

// Without a pull request waiting there is nothing for --pending to work on, and saying so is
// better than regenerating what the registry holds by surprise.
func TestRegenerate_pendingWithNothingWaiting(t *testing.T) {
	t.Parallel()
	held := regenerateRegistry{config: &aquag2.Config{}}
	reg := &waitingRegistry{regenerateRegistry: held}
	c := New(ghClient(t), nil, nil, reg, failingAutoMerger{}, nil, nil)
	_, err := c.Regenerate(t.Context(), discardLogger(), &RegenerateInput{
		PkgName: "ogham/exa",
		Pending: true,
	})
	if !errors.Is(err, errNothingWaiting) {
		t.Fatalf("got %v, want it to say nothing is waiting", err)
	}
}
