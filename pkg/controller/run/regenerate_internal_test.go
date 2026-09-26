package run

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
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
		inFlight: map[string]struct{}{g2.HeadBranchName("cli/cli"): {}},
		config:   &aquag2.Config{},
	}
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
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
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
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
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
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
	c := New(ghClient(t), nil, reg, failingAutoMerger{}, nil, nil)
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
