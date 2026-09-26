package add

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
	gogithub "github.com/google/go-github/v92/github"
)

// fakeRegistry stands in for aqua-registry-g2 and records what was written to it.
type fakeRegistry struct {
	config    *aquag2.Config
	committed []*g2.File
	created   int
}

func (f *fakeRegistry) Config(_ context.Context, _ string) (*aquag2.Config, error) {
	return f.config, nil
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
	return &gogithub.PullRequest{Number: new(1)}, nil
}

// fakeRepos is the repository the description is read from.
type fakeRepos struct {
	description string
	err         error
}

func (f *fakeRepos) Get(_ context.Context, _, _ string) (*gogithub.Repository, *gogithub.Response, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &gogithub.Repository{Description: new(f.description)}, nil, nil
}

func logger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// A package the registry doesn't have gets a definition and a place in the order.
func TestController_Add(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	s := state.New()
	s.Packages["sst/opencode"] = &state.Package{Round: 3}

	changed, err := New(reg, &fakeRepos{description: "GitHub's official command line tool."}).Add(
		t.Context(), logger(), &Input{PkgName: "cli/cli", State: s})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("the state gained a package, so it has to be stored")
	}
	if reg.created != 1 {
		t.Errorf("opened %d pull requests", reg.created)
	}
	if len(reg.committed) != 1 || reg.committed[0].Path != g2.ConfigFileName {
		t.Fatalf("committed %v", reg.committed)
	}
	content := reg.committed[0].Content
	for _, want := range []string{
		"type: github_release",
		"repo_owner: cli",
		"repo_name: cli",
		"description: GitHub's official command line tool",
		"- name: cli",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the definition doesn't say %q:\n%s", want, content)
		}
	}
}

// It joins at the front of the order: this lap's turn, rather than jumping the whole
// registry or losing a lap for having arrived late.
func TestController_Add_joinsTheOrder(t *testing.T) {
	t.Parallel()
	s := state.New()
	s.Packages["sst/opencode"] = &state.Package{Round: 3}
	s.Packages["cli/gh-dash"] = &state.Package{Round: 4}

	if _, err := New(&fakeRegistry{}, &fakeRepos{}).Add(t.Context(), logger(),
		&Input{PkgName: "cli/cli", State: s}); err != nil {
		t.Fatal(err)
	}
	pkg := s.Packages["cli/cli"]
	if pkg == nil {
		t.Fatal("the package isn't in the order")
	}
	if pkg.Round != 3 {
		t.Errorf("it joined with %d turns, want 3", pkg.Round)
	}
	if pkg.RepoOwner != "cli" || pkg.RepoName != "cli" {
		t.Errorf("the order says %s/%s", pkg.RepoOwner, pkg.RepoName)
	}
}

// A package that is one command of a repository takes the repository it was given, and
// the command from the name.
func TestController_Add_commandOfARepository(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	s := state.New()
	if _, err := New(reg, &fakeRepos{}).Add(t.Context(), logger(), &Input{
		PkgName: "kubernetes/kubernetes/kubectl",
		Repo:    "kubernetes/kubernetes",
		State:   s,
	}); err != nil {
		t.Fatal(err)
	}
	content := reg.committed[0].Content
	for _, want := range []string{"repo_name: kubernetes", "- name: kubectl"} {
		if !strings.Contains(content, want) {
			t.Errorf("the definition doesn't say %q:\n%s", want, content)
		}
	}
	if pkg := s.Packages["kubernetes/kubernetes/kubectl"]; pkg == nil || pkg.RepoName != "kubernetes" {
		t.Errorf("the order says %v", pkg)
	}
}

// Named commands are what the definition says, however many.
func TestController_Add_commands(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	if _, err := New(reg, &fakeRepos{}).Add(t.Context(), logger(), &Input{
		PkgName:  "astral-sh/uv",
		Commands: []string{"uv", "uvx"},
		State:    state.New(),
	}); err != nil {
		t.Fatal(err)
	}
	content := reg.committed[0].Content
	if !strings.Contains(content, "- name: uv\n") || !strings.Contains(content, "- name: uvx") {
		t.Errorf("the definition doesn't name both commands:\n%s", content)
	}
}

// A branch that already holds a definition keeps it. Writing over it would replace a
// definition somebody reviewed with one inferred from a name.
func TestController_Add_definitionAlreadyThere(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{config: &aquag2.Config{}}
	s := state.New()
	changed, err := New(reg, &fakeRepos{}).Add(t.Context(), logger(), &Input{PkgName: "cli/cli", State: s})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) != 0 || reg.created != 0 {
		t.Error("the definition was written over")
	}
	// The other half is still done: this is what running it again after a failure is
	// for.
	if !changed || s.Packages["cli/cli"] == nil {
		t.Error("the package didn't join the order")
	}
}

// A package already in the order doesn't join it twice, and its turns are left alone.
func TestController_Add_alreadyInTheOrder(t *testing.T) {
	t.Parallel()
	s := state.New()
	s.Packages["cli/cli"] = &state.Package{Round: 7, Stars: 1}
	changed, err := New(&fakeRegistry{config: &aquag2.Config{}}, &fakeRepos{}).Add(
		t.Context(), logger(), &Input{PkgName: "cli/cli", State: s})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("nothing changed, so nothing has to be stored")
	}
	if s.Packages["cli/cli"].Round != 7 {
		t.Errorf("its turns became %d", s.Packages["cli/cli"].Round)
	}
}

// A dry run writes nothing at all, including the order.
func TestController_Add_dryRun(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	s := state.New()
	changed, err := New(reg, &fakeRepos{}).Add(t.Context(), logger(), &Input{
		PkgName: "cli/cli", State: s, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(s.Packages) != 0 || reg.created != 0 || len(reg.committed) != 0 {
		t.Error("a dry run wrote something")
	}
}

// A name that isn't a repository and has no --repo is refused.
func TestController_Add_noRepo(t *testing.T) {
	t.Parallel()
	_, err := New(&fakeRegistry{}, &fakeRepos{}).Add(t.Context(), logger(), &Input{
		PkgName: "cli", State: state.New(),
	})
	if !errors.Is(err, errNoRepo) {
		t.Fatalf("got %v", err)
	}
}

// A repository that can't be read leaves the description out rather than failing: the
// catalogue can carry a package without one, and a package nobody can add is worse.
func TestController_Add_noDescription(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	if _, err := New(reg, &fakeRepos{err: errors.New("404")}).Add(t.Context(), logger(), &Input{
		PkgName: "cli/cli", State: state.New(),
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reg.committed[0].Content, "description") {
		t.Errorf("the definition has a description:\n%s", reg.committed[0].Content)
	}
}
