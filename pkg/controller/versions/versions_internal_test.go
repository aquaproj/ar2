package versions

import (
	"context"
	"log/slog"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v93/github"
)

// fakeRegistry holds the lists the base branch should have and what each ref holds.
type fakeRegistry struct {
	lists     map[string]*g2.File
	held      map[string]string
	pr        *gogithub.PullRequest
	committed []*g2.File
	branch    string
	parent    string
	opened    int
	stale     []string
	listRef   string
}

func (f *fakeRegistry) StaleVersionsLists(_ context.Context, _ *slog.Logger, _ g2.Blobs, listRef string) ([]string, error) {
	f.listRef = listRef
	return f.stale, nil
}

func (f *fakeRegistry) VersionsListFile(_ context.Context, _ *slog.Logger, _, id string) (*g2.File, error) {
	return f.lists[id], nil
}

func (f *fakeRegistry) File(_ context.Context, ref, path string) (string, error) {
	return f.held[ref+":"+path], nil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, branch string) (string, error) {
	return "tip-of-" + branch, nil
}

func (f *fakeRegistry) Commit(_ context.Context, branch, parent, _ string, files []*g2.File) error {
	f.branch, f.parent, f.committed = branch, parent, files
	return nil
}

func (f *fakeRegistry) VersionsPullRequest(context.Context) (*gogithub.PullRequest, error) {
	return f.pr, nil
}

func (f *fakeRegistry) CreateVersionsPullRequest(context.Context, *slog.Logger, string, string, string) (*gogithub.PullRequest, error) {
	f.opened++
	return &gogithub.PullRequest{}, nil
}

type recordingAutoMerger struct{ enabled int }

func (r *recordingAutoMerger) EnableAutoMerge(context.Context, string) error {
	r.enabled++
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func paths(files []*g2.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// The lists that are behind go into a pull request of their own from the base branch, which
// merges itself.
func TestController_Update(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{lists: map[string]*g2.File{
		"1790772767": {Path: "pkgs/67/1790772767/versions.json", Content: "new"},
	}}
	merger := &recordingAutoMerger{}
	if err := New(reg, merger, nil, "main").Update(context.Background(), discardLogger(), []string{"1790772767", "1791650998"}); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"pkgs/67/1790772767/versions.json"}, paths(reg.committed)); diff != "" {
		t.Errorf("what was committed (-want +got):\n%s", diff)
	}
	if reg.branch != g2.VersionsBranch || reg.parent != "tip-of-main" {
		t.Errorf("committed onto %s from %s", reg.branch, reg.parent)
	}
	if reg.opened != 1 || merger.enabled != 1 {
		t.Errorf("%d pull requests opened, auto-merge turned on %d times", reg.opened, merger.enabled)
	}
}

// With a pull request open, the lists go onto it, and one it already holds as made isn't
// committed again.
func TestController_Update_openPullRequest(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		lists: map[string]*g2.File{
			"1790772767": {Path: "pkgs/67/1790772767/versions.json", Content: "new"},
			"1791650998": {Path: "pkgs/98/1791650998/versions.json", Content: "same"},
		},
		held: map[string]string{g2.VersionsBranch + ":pkgs/98/1791650998/versions.json": "same"},
		pr:   &gogithub.PullRequest{},
	}
	if err := New(reg, nil, nil, "main").Update(context.Background(), discardLogger(), []string{"1790772767", "1791650998"}); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"pkgs/67/1790772767/versions.json"}, paths(reg.committed)); diff != "" {
		t.Errorf("what was committed (-want +got):\n%s", diff)
	}
	if reg.parent != "tip-of-"+g2.VersionsBranch || reg.opened != 0 {
		t.Errorf("committed from %s, %d pull requests opened", reg.parent, reg.opened)
	}
}

// Lists in step with their directories commit nothing, and what isn't an id is refused.
func TestController_Update_nothingToDo(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	if err := New(reg, nil, nil, "main").Update(context.Background(), discardLogger(), []string{"1790772767"}); err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil || reg.opened != 0 {
		t.Errorf("committed %v, opened %d", reg.committed, reg.opened)
	}
	dry := &fakeRegistry{lists: map[string]*g2.File{"1790772767": {Path: "pkgs/67/1790772767/versions.json", Content: "new"}}}
	c := New(dry, nil, nil, "main")
	c.DryRun()
	if err := c.Update(context.Background(), discardLogger(), []string{"1790772767"}); err != nil {
		t.Fatal(err)
	}
	if dry.committed != nil || dry.opened != 0 {
		t.Errorf("a dry run committed %v, opened %d", dry.committed, dry.opened)
	}
	if err := New(reg, nil, nil, "main").Update(context.Background(), discardLogger(), []string{"cli/cli"}); err == nil {
		t.Error("a package name was taken for an id")
	}
}

// No ids means the lists that are behind, asked of the base branch when no pull request is
// open.
func TestController_Update_stale(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		stale: []string{"1790772767"},
		lists: map[string]*g2.File{"1790772767": {Path: "pkgs/67/1790772767/versions.json", Content: "new"}},
	}
	if err := New(reg, &recordingAutoMerger{}, nil, "main").Update(context.Background(), discardLogger(), nil); err != nil {
		t.Fatal(err)
	}
	if reg.listRef != "main" {
		t.Errorf("behind was asked of %s", reg.listRef)
	}
	if diff := cmp.Diff([]string{"pkgs/67/1790772767/versions.json"}, paths(reg.committed)); diff != "" {
		t.Errorf("what was committed (-want +got):\n%s", diff)
	}
}

// With a pull request open, behind is asked of the lists it holds, and its auto-merge is
// turned on when it is off, even with nothing to add.
func TestController_Update_staleOnOpenPullRequest(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{pr: &gogithub.PullRequest{}}
	merger := &recordingAutoMerger{}
	if err := New(reg, merger, nil, "main").Update(context.Background(), discardLogger(), nil); err != nil {
		t.Fatal(err)
	}
	if reg.listRef != g2.VersionsBranch {
		t.Errorf("behind was asked of %s", reg.listRef)
	}
	if reg.committed != nil || merger.enabled != 1 {
		t.Errorf("committed %v, auto-merge turned on %d times", reg.committed, merger.enabled)
	}

	on := &fakeRegistry{pr: &gogithub.PullRequest{AutoMerge: &gogithub.PullRequestAutoMerge{}}}
	onMerger := &recordingAutoMerger{}
	if err := New(on, onMerger, nil, "main").Update(context.Background(), discardLogger(), nil); err != nil {
		t.Fatal(err)
	}
	if onMerger.enabled != 0 {
		t.Errorf("auto-merge already on was turned on %d times", onMerger.enabled)
	}
}
