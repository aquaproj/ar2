package versions

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeRegistry stands in for aqua-registry-g2 and records what was pushed to it.
type fakeRegistry struct {
	versions map[string]map[string]struct{}
	files    map[string]string
	// tree is the sha of each branch's versions directory, which is what says whether a
	// list is still of what the branch holds.
	tree   map[string]string
	pushed []*g2.File
	parent string
	pushes int
	// read counts the files read, which is what says a branch answered for by its sha
	// alone cost nothing more.
	read int
}

func (f *fakeRegistry) VersionsTree(_ context.Context, ref string) (string, error) {
	return f.tree[ref], nil
}

func (f *fakeRegistry) Branch(pkgName string) (string, bool) {
	branch, ok := map[string]string{"cli/cli": "pkg_1"}[pkgName]
	return branch, ok
}

func (f *fakeRegistry) BranchSHA(_ context.Context, _ string) (string, error) {
	return "parent-sha", nil
}

func (f *fakeRegistry) VersionsOnRef(_ context.Context, _ *slog.Logger, ref string) (map[string]struct{}, error) {
	return f.versions[ref], nil
}

func (f *fakeRegistry) File(_ context.Context, _, path string) (string, error) {
	f.read++
	return f.files[path], nil
}

func (f *fakeRegistry) Push(_ context.Context, _, parent, _ string, files []*g2.File) error {
	f.parent = parent
	f.pushed = files
	f.pushes++
	return nil
}

// held is a registry.json as the registry holds it: one line, as a generation writes it.
func held(t *testing.T, publishedAt string) string {
	t.Helper()
	reg := &aquag2.Registry{
		PublishedAt: publishedAt,
		Assets:      []*aquag2.Asset{{OS: "linux", Arch: "amd64", Asset: "gh.tar.gz"}},
	}
	b, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func registryOf(t *testing.T) *fakeRegistry {
	t.Helper()
	return &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_1": {"v2.100.0": {}, "v2.101.0": {}}},
		tree:     map[string]string{"pkg_1": "versions-tree-sha"},
		files: map[string]string{
			"versions/v2.100.0/registry-1.json": held(t, "2026-09-01T00:00:00Z"),
			"versions/v2.101.0/registry-1.json": held(t, "2026-09-15T14:24:34Z"),
		},
	}
}

func defs() map[string]string {
	return map[string]string{"pkg_1": "name: cli/cli\ntype: github_release\n"}
}

// The list is the branch's versions, newest release first, each with the date its release
// was published and the digest of the file the registry serves.
func TestWrite(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())

	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}

	if reg.pushes != 1 {
		t.Fatalf("the branch was pushed to %d times", reg.pushes)
	}
	if reg.parent != "parent-sha" {
		t.Errorf("the commit's parent is %q", reg.parent)
	}
	if len(reg.pushed) != 1 || reg.pushed[0].Path != "versions.json" {
		t.Fatalf("what was pushed is %+v", reg.pushed)
	}
	got, err := aquag2.ReadVersions(strings.NewReader(reg.pushed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "versions-tree-sha" {
		t.Errorf("the source is %q", got.Source)
	}
	want := []*aquag2.Version{
		{
			Version:     "v2.101.0",
			PublishedAt: "2026-09-15T14:24:34Z",
			Digest:      digest(held(t, "2026-09-15T14:24:34Z")),
		},
		{
			Version:     "v2.100.0",
			PublishedAt: "2026-09-01T00:00:00Z",
			Digest:      digest(held(t, "2026-09-01T00:00:00Z")),
		},
	}
	if diff := cmp.Diff(want, got.Versions); diff != "" {
		t.Error(diff)
	}
}

// A branch whose list is of the versions directory it holds is answered for by the
// directory's sha and the list naming it: the versions aren't read, nor a file per version,
// and nothing is written. A sweep over a registry that is up to date costs two requests a
// package.
func TestWrite_alreadyListed(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	listed := reg.pushed[0].Content

	reg2 := registryOf(t)
	reg2.files["versions.json"] = listed
	c2 := New(reg2, defs())
	if err := c2.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg2.pushes != 0 {
		t.Errorf("the branch was pushed to %d times for a list it already holds", reg2.pushes)
	}
	if reg2.read != 1 {
		t.Errorf("%d files were read where the list alone answers", reg2.read)
	}
}

// A branch whose directory has moved on is read and written again. The list names the
// directory it was made from, so the next run is cheap again.
func TestWrite_sourceMoved(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	reg.files["versions.json"] = reg.pushed[0].Content
	reg.tree["pkg_1"] = "another-versions-tree-sha"
	reg.pushes = 0
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 1 {
		t.Fatalf("the branch was pushed to %d times", reg.pushes)
	}
	got, err := aquag2.ReadVersions(strings.NewReader(reg.pushed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "another-versions-tree-sha" {
		t.Errorf("the source is %q", got.Source)
	}
}

// A branch holding no versions has no list to write: an empty one would say the registry
// holds nothing of a package it has just taken over.
func TestWrite_noVersions(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	reg.tree["pkg_1"] = ""
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 {
		t.Errorf("the branch was pushed to %d times", reg.pushes)
	}
}

// A version gained since the list was written is what makes it not the branch's list.
func TestWrite_versionAdded(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}

	reg.files["versions.json"] = reg.pushed[0].Content
	reg.versions["pkg_1"]["v2.102.0"] = struct{}{}
	reg.files["versions/v2.102.0/registry-1.json"] = held(t, "2026-09-30T00:00:00Z")
	// The directory holds another version, so its sha is another sha.
	reg.tree["pkg_1"] = "versions-tree-sha-with-v2.102.0"
	reg.pushes = 0
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 1 {
		t.Fatalf("the branch was pushed to %d times", reg.pushes)
	}
	got, err := aquag2.ReadVersions(strings.NewReader(reg.pushed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"v2.102.0", "v2.101.0", "v2.100.0"}, got.Tags()); diff != "" {
		t.Error(diff)
	}
}

// A dry run says what it would write and writes nothing.
func TestWrite_dryRun(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 {
		t.Errorf("a dry run pushed %d times", reg.pushes)
	}
}

// A branch is what a branch's own workflow knows, so it can be named instead of a package.
func TestWrite_namedBranch(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, nil)
	if err := c.Write(context.Background(), discardLogger(), &Args{Branches: []string{"pkg_1"}}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 1 {
		t.Errorf("the named branch was pushed to %d times", reg.pushes)
	}
}

// A named package is resolved to its branch, and one the registry doesn't hold is said
// rather than ignored.
func TestWrite_namedPackage(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{Packages: []string{"cli/cli", "nothing/here"}}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 1 {
		t.Errorf("the named package was pushed to %d times", reg.pushes)
	}
}

// A version whose directory holds no registry.json is still a version the branch holds, so
// it is listed with what can't be read about it left out.
func TestWrite_noFile(t *testing.T) {
	t.Parallel()
	reg := registryOf(t)
	reg.versions["pkg_1"]["v2.102.0"] = struct{}{}
	c := New(reg, defs())
	if err := c.Write(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	got, err := aquag2.ReadVersions(strings.NewReader(reg.pushed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	want := &aquag2.Version{Version: "v2.102.0"}
	if diff := cmp.Diff(want, got.Versions[len(got.Versions)-1]); diff != "" {
		t.Error(diff)
	}
}
