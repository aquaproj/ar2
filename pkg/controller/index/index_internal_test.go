package index

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/szksh-lab-2/ar2/pkg/g2"
)

// fakeRegistry stands in for aqua-registry-g2 and records what was written to it.
type fakeRegistry struct {
	index      *aquag2.Index
	branches   []string
	configs    map[string]*aquag2.Config
	openPR     *gogithub.PullRequest
	readRef    string
	committed  []*g2.File
	parent     string
	createdPRs int
	configRead []string
}

func (f *fakeRegistry) Index(_ context.Context, ref string) (*aquag2.Index, error) {
	f.readRef = ref
	if f.index == nil {
		return &aquag2.Index{}, nil
	}
	return f.index, nil
}

func (f *fakeRegistry) PackageBranches(_ context.Context) ([]string, error) {
	return f.branches, nil
}

func (f *fakeRegistry) Config(_ context.Context, pkgName string) (*aquag2.Config, error) {
	f.configRead = append(f.configRead, pkgName)
	return f.configs[pkgName], nil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, _ string) (string, error) {
	return "parent-sha", nil
}

func (f *fakeRegistry) Commit(_ context.Context, _, parent, _ string, files []*g2.File) error {
	f.parent = parent
	f.committed = files
	return nil
}

func (f *fakeRegistry) IndexPullRequest(_ context.Context) (*gogithub.PullRequest, error) {
	return f.openPR, nil
}

func (f *fakeRegistry) CreateIndexPullRequest(_ context.Context, _, _, _ string) (*gogithub.PullRequest, error) {
	f.createdPRs++
	return &gogithub.PullRequest{Number: new(1)}, nil
}

func config(description string) *aquag2.Config {
	return &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{Description: description}}
}

func logger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestController_Add(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{configs: map[string]*aquag2.Config{
		"cli/cli": config("GitHub's official command line tool"),
	}}
	if err := New(reg, "main").Add(t.Context(), logger(), "pkg_cli_2fcli"); err != nil {
		t.Fatal(err)
	}
	if reg.createdPRs != 1 {
		t.Errorf("opened %d pull requests, want 1", reg.createdPRs)
	}
	if len(reg.committed) != 1 || reg.committed[0].Path != g2.IndexFileName {
		t.Fatalf("committed %+v, want the catalogue", reg.committed)
	}
	if !strings.Contains(reg.committed[0].Content, "cli/cli") {
		t.Errorf("the catalogue doesn't hold the package:\n%s", reg.committed[0].Content)
	}
	// Only the branch that was given: adding one package lists nothing.
	if diff := cmp.Diff([]string{"cli/cli"}, reg.configRead); diff != "" {
		t.Errorf("the definitions read are wrong (-want +got):\n%s", diff)
	}
}

// A ref that doesn't hold a package is turned away. The workflow passes whichever ref
// was created, so this is how a tag or an operational branch arrives.
func TestController_Add_notAPackageBranch(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	if err := New(reg, "main").Add(t.Context(), logger(), "ar2_index"); err == nil {
		t.Fatal("an error must be returned")
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}

// A package already in the catalogue costs nothing: its definition isn't read and
// nothing is committed.
func TestController_Add_alreadyThere(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{index: &aquag2.Index{Packages: []*aquag2.IndexPackage{{Name: "cli/cli"}}}}
	if err := New(reg, "main").Add(t.Context(), logger(), "pkg_cli_2fcli"); err != nil {
		t.Fatal(err)
	}
	if len(reg.configRead) != 0 {
		t.Errorf("read %v, want nothing", reg.configRead)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}

func TestController_Sync(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		branches: []string{"cli/cli", "suzuki-shunsuke/tfcmt"},
		index:    &aquag2.Index{Packages: []*aquag2.IndexPackage{{Name: "cli/cli"}}},
		configs: map[string]*aquag2.Config{
			"suzuki-shunsuke/tfcmt": config("Fork of tfnotify"),
		},
	}
	if err := New(reg, "main").Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	// Only what the catalogue is missing: the definition of a package it has isn't
	// worth a request.
	if diff := cmp.Diff([]string{"suzuki-shunsuke/tfcmt"}, reg.configRead); diff != "" {
		t.Errorf("the definitions read are wrong (-want +got):\n%s", diff)
	}
	if reg.createdPRs != 1 {
		t.Errorf("opened %d pull requests, want 1", reg.createdPRs)
	}
}

// An open pull request is added to rather than replaced, and the catalogue is read
// from it. Reading the default branch would make this run undo the last one.
func TestController_Add_addsToTheOpenPullRequest(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		openPR:  &gogithub.PullRequest{Number: new(7)},
		index:   &aquag2.Index{Packages: []*aquag2.IndexPackage{{Name: "aquaproj/aqua"}}},
		configs: map[string]*aquag2.Config{"cli/cli": config("GitHub's official command line tool")},
	}
	if err := New(reg, "main").Add(t.Context(), logger(), "pkg_cli_2fcli"); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(g2.IndexBranch, reg.readRef); diff != "" {
		t.Errorf("the catalogue was read from the wrong ref (-want +got):\n%s", diff)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
	// Both packages survive: the one waiting to merge and the one just added.
	for _, name := range []string{"aquaproj/aqua", "cli/cli"} {
		if !strings.Contains(reg.committed[0].Content, name) {
			t.Errorf("the catalogue lost %s:\n%s", name, reg.committed[0].Content)
		}
	}
}

// A branch with nothing generated onto it yet has no definition to describe the
// package with. The run that writes one brings it here.
func TestController_Sync_noConfigYet(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{branches: []string{"cli/cli"}}
	if err := New(reg, "main").Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}
