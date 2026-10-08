package template

import (
	"context"
	"log/slog"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeRegistry stands in for aqua-registry-g2 and records what was pushed to it.
type fakeRegistry struct {
	template map[string]*gogithub.TreeEntry
	// blobs is what each branch holds, by branch then path.
	blobs    map[string]map[string]string
	branches []*g2.Branch
	pushed   map[string][]*gogithub.TreeEntry
	parents  map[string]string
	asked    int
}

func (f *fakeRegistry) Template(_ context.Context) (map[string]*gogithub.TreeEntry, error) {
	return f.template, nil
}

func (f *fakeRegistry) PackageBranches(_ context.Context) ([]*g2.Branch, error) {
	return f.branches, nil
}

func (f *fakeRegistry) BlobSHAs(_ context.Context, ref string) (map[string]string, error) {
	return f.blobs[ref], nil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, branch string) (string, error) {
	f.asked++
	for _, b := range f.branches {
		if b.Name == branch {
			return b.SHA, nil
		}
	}
	return "", nil
}

func (f *fakeRegistry) PushEntries(_ context.Context, branch, parent, _ string, entries []*gogithub.TreeEntry) error {
	if f.pushed == nil {
		f.pushed = map[string][]*gogithub.TreeEntry{}
		f.parents = map[string]string{}
	}
	f.pushed[branch] = entries
	f.parents[branch] = parent
	return nil
}

func entry(path, sha string) *gogithub.TreeEntry {
	mode, typ := "100644", "blob"
	return &gogithub.TreeEntry{Path: &path, SHA: &sha, Mode: &mode, Type: &typ}
}

func registryOf() *fakeRegistry {
	return &fakeRegistry{
		template: map[string]*gogithub.TreeEntry{
			".github/workflows/test.yaml":     entry(".github/workflows/test.yaml", "test-blob"),
			".github/workflows/versions.yaml": entry(".github/workflows/versions.yaml", "versions-blob"),
		},
		blobs: map[string]map[string]string{
			// A branch made before the versions caller existed.
			"pkg_1": {".github/workflows/test.yaml": "test-blob", "registry.yaml": "its own"},
			// One holding both, and whatever else is its own.
			"pkg_2": {
				".github/workflows/test.yaml":     "test-blob",
				".github/workflows/versions.yaml": "versions-blob",
				"versions/v1.0.0/registry-1.json": "a version",
			},
		},
		branches: []*g2.Branch{{Name: "pkg_1", SHA: "sha-1"}, {Name: "pkg_2", SHA: "sha-2"}},
	}
}

// The branch missing a template file is written to, with the blob the default branch
// holds, and the branch holding the template is left alone.
func TestSync(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	c := New(reg)

	if err := c.Sync(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}

	if _, ok := reg.pushed["pkg_2"]; ok {
		t.Error("the branch holding the template was written to")
	}
	want := []*gogithub.TreeEntry{entry(".github/workflows/versions.yaml", "versions-blob")}
	if diff := cmp.Diff(want, reg.pushed["pkg_1"]); diff != "" {
		t.Error(diff)
	}
	if reg.parents["pkg_1"] != "sha-1" {
		t.Errorf("the commit's parent is %q", reg.parents["pkg_1"])
	}
	// The listing said where each branch is, so nothing had to be asked for it.
	if reg.asked != 0 {
		t.Errorf("the branches were asked about %d times", reg.asked)
	}
}

// A file whose call has changed is a blob the branch doesn't hold, so it is written over.
func TestSync_fileChanged(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	reg.blobs["pkg_2"][".github/workflows/versions.yaml"] = "the blob it had before"
	c := New(reg)
	if err := c.Sync(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	want := []*gogithub.TreeEntry{entry(".github/workflows/versions.yaml", "versions-blob")}
	if diff := cmp.Diff(want, reg.pushed["pkg_2"]); diff != "" {
		t.Error(diff)
	}
}

// A run told which branch to write doesn't know where it is, so it asks.
func TestSync_namedBranch(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	c := New(reg)
	if err := c.Sync(context.Background(), discardLogger(), &Args{Branches: []string{"pkg_1"}}); err != nil {
		t.Fatal(err)
	}
	if reg.parents["pkg_1"] != "sha-1" {
		t.Errorf("the commit's parent is %q", reg.parents["pkg_1"])
	}
	if reg.asked != 1 {
		t.Errorf("the branch was asked about %d times", reg.asked)
	}
}

// A branch that isn't there is nothing to write to.
func TestSync_noSuchBranch(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	c := New(reg)
	if err := c.Sync(context.Background(), discardLogger(), &Args{Branches: []string{"pkg_nothing"}}); err != nil {
		t.Fatal(err)
	}
	if len(reg.pushed) != 0 {
		t.Errorf("what was pushed is %+v", reg.pushed)
	}
}

// A dry run says what it would write and writes nothing.
func TestSync_dryRun(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	c := New(reg)
	if err := c.Sync(context.Background(), discardLogger(), &Args{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(reg.pushed) != 0 {
		t.Errorf("a dry run pushed %+v", reg.pushed)
	}
}

// A limit bounds how many branches one run writes to.
func TestSync_limit(t *testing.T) {
	t.Parallel()
	reg := registryOf()
	reg.blobs["pkg_2"] = map[string]string{}
	c := New(reg)
	if err := c.Sync(context.Background(), discardLogger(), &Args{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if len(reg.pushed) != 1 {
		t.Errorf("a run limited to one branch pushed to %d", len(reg.pushed))
	}
}
