package dates

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeRegistry stands in for aqua-registry-g2 and records what was pushed to it.
type fakeRegistry struct {
	// versions is what each branch holds, by branch name.
	versions map[string]map[string]struct{}
	// files is what the branches hold, by path.
	files  map[string]string
	pushed []*g2.File
	parent string
	pushes int
}

func (f *fakeRegistry) Branch(pkgName string) (string, bool) {
	return "pkg_" + pkgName, true
}

func (f *fakeRegistry) BranchSHA(_ context.Context, _ string) (string, error) {
	return "parent-sha", nil
}

func (f *fakeRegistry) VersionsOnRef(_ context.Context, _ *slog.Logger, ref string) (map[string]struct{}, error) {
	return f.versions[ref], nil
}

func (f *fakeRegistry) File(_ context.Context, _, path string) (string, error) {
	return f.files[path], nil
}

func (f *fakeRegistry) Push(_ context.Context, _, parent, _ string, files []*g2.File) error {
	f.parent = parent
	f.pushed = files
	f.pushes++
	return nil
}

// fakeReleases answers with pages of releases and counts the requests, which is what says
// whether the listing stopped as soon as it had what it came for.
type fakeReleases struct {
	pages    [][]*gogithub.RepositoryRelease
	requests int
}

func (f *fakeReleases) ListReleases(_ context.Context, _, _ string, opts *gogithub.ListOptions) ([]*gogithub.RepositoryRelease, *gogithub.Response, error) {
	f.requests++
	page := opts.Page
	if page == 0 {
		page = 1
	}
	resp := &gogithub.Response{}
	if page < len(f.pages) {
		resp.NextPage = page + 1
	}
	return f.pages[page-1], resp, nil
}

func release(tag string, at time.Time) *gogithub.RepositoryRelease {
	return &gogithub.RepositoryRelease{
		TagName:     tag,
		PublishedAt: &gogithub.Timestamp{Time: at},
	}
}

// held is a registry.json as the registry holds it: one line, as a generation writes it.
func held(t *testing.T, reg *aquag2.Registry) string {
	t.Helper()
	b, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func definition(pkgName string) string {
	return "name: " + pkgName + "\ntype: github_release\nrepo_owner: cli\nrepo_name: cli\n"
}

func registryOf() *aquag2.Registry {
	return &aquag2.Registry{Assets: []*aquag2.Asset{{OS: "linux", Arch: "amd64", Asset: "gh.tar.gz"}}}
}

// A version whose file doesn't say when it was published is given the date of its release,
// and the file is written the way a generation writes it.
func TestFill(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 15, 14, 24, 34, 0, time.UTC)
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_cli/cli": {"v2.101.0": {}}},
		files:    map[string]string{"versions/v2.101.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{release("v2.101.0", at)}}}
	c := New(reg, releases, map[string]string{"pkg_cli/cli": definition("cli/cli")})

	if err := c.Fill(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}

	if reg.pushes != 1 {
		t.Fatalf("the branch was pushed to %d times", reg.pushes)
	}
	if reg.parent != "parent-sha" {
		t.Errorf("the commit's parent is %q", reg.parent)
	}
	want := registryOf()
	want.PublishedAt = "2026-09-15T14:24:34Z"
	if diff := cmp.Diff([]*g2.File{{Path: "versions/v2.101.0/registry-1.json", Content: held(t, want)}}, reg.pushed); diff != "" {
		t.Error(diff)
	}
}

// A version that already says it is left alone, and a registry with nothing to fill in is
// not written to at all.
func TestFill_alreadyDated(t *testing.T) {
	t.Parallel()
	dated := registryOf()
	dated.PublishedAt = "2026-09-15T14:24:34Z"
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_cli/cli": {"v2.101.0": {}}},
		files:    map[string]string{"versions/v2.101.0/registry-1.json": held(t, dated)},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{}}}
	c := New(reg, releases, map[string]string{"pkg_cli/cli": definition("cli/cli")})

	if err := c.Fill(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 {
		t.Errorf("the branch was pushed to %d times", reg.pushes)
	}
	if releases.requests != 0 {
		t.Errorf("the releases were listed %d times for a registry with nothing to fill in", releases.requests)
	}
}

// A version the repository has no release for is left saying nothing, which is what it
// already said.
func TestFill_noRelease(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_cli/cli": {"v2.101.0": {}}},
		files:    map[string]string{"versions/v2.101.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{release("v2.100.0", time.Now())}}}
	c := New(reg, releases, map[string]string{"pkg_cli/cli": definition("cli/cli")})

	if err := c.Fill(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 {
		t.Errorf("the branch was pushed to %d times", reg.pushes)
	}
}

// A dry run says what it would write and writes nothing.
func TestFill_dryRun(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_cli/cli": {"v2.101.0": {}}},
		files:    map[string]string{"versions/v2.101.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{release("v2.101.0", time.Now())}}}
	c := New(reg, releases, map[string]string{"pkg_cli/cli": definition("cli/cli")})

	if err := c.Fill(context.Background(), discardLogger(), &Args{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 {
		t.Errorf("a dry run pushed %d times", reg.pushes)
	}
}

// The listing stops as soon as every version asked about has been found, so a package with
// a long history and one undated version near the top of it costs one page.
func TestFill_stopsListing(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 15, 14, 24, 34, 0, time.UTC)
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_cli/cli": {"v2.101.0": {}}},
		files:    map[string]string{"versions/v2.101.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{
		{release("v2.101.0", at)},
		{release("v2.100.0", at)},
	}}
	c := New(reg, releases, map[string]string{"pkg_cli/cli": definition("cli/cli")})

	if err := c.Fill(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if releases.requests != 1 {
		t.Errorf("the releases were listed %d times", releases.requests)
	}
}

// The versions of a package whose versions are its tags aren't releases, so there is no
// release list to read a date from and the package is left alone.
func TestFill_versionsAreTags(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{"pkg_a/b": {"v1.0.0": {}}},
		files:    map[string]string{"versions/v1.0.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{}}}
	def := "name: a/b\ntype: github_release\nrepo_owner: a\nrepo_name: b\nversion_source: github_tag\n"
	c := New(reg, releases, map[string]string{"pkg_a/b": def})

	if err := c.Fill(context.Background(), discardLogger(), &Args{}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 0 || releases.requests != 0 {
		t.Errorf("pushes=%d release listings=%d", reg.pushes, releases.requests)
	}
}

// A limit bounds how many packages one run writes to.
func TestFill_limit(t *testing.T) {
	t.Parallel()
	at := time.Now()
	reg := &fakeRegistry{
		versions: map[string]map[string]struct{}{
			"pkg_a/a": {"v1.0.0": {}},
			"pkg_b/b": {"v1.0.0": {}},
		},
		files: map[string]string{"versions/v1.0.0/registry-1.json": held(t, registryOf())},
	}
	releases := &fakeReleases{pages: [][]*gogithub.RepositoryRelease{{release("v1.0.0", at)}}}
	c := New(reg, releases, map[string]string{
		"pkg_a/a": definition("a/a"),
		"pkg_b/b": definition("b/b"),
	})

	if err := c.Fill(context.Background(), discardLogger(), &Args{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if reg.pushes != 1 {
		t.Errorf("a run limited to one package pushed %d times", reg.pushes)
	}
}
