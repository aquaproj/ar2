package g2_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

const (
	testID  = "1790772767"
	testDir = "pkgs/67/1790772767"
)

// fakeGit is the part of GitHub's Git data API a commit goes through, held in memory.
//
// Trees are what the tests look at: each tree created is recorded with its entries, and the
// sha of a versions directory is whatever the test says the ref holds.
type fakeGit struct {
	mu sync.Mutex
	// versionsTrees is the sha of the package's versions directory, by the ref asked.
	versionsTrees map[string]string
	// files is the content of a file, by "ref:path".
	files map[string]string
	// versionLists are the versions directory listings, by ref.
	versionLists map[string][]string
	// trees are the entries of each tree created, in order, and bases the tree each was
	// created on top of.
	trees [][]*gogithub.TreeEntry
	bases []string
	// moved is the branch that was pointed at a commit, and the commit.
	moved, movedTo string
}

func (f *fakeGit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/repos/aquaproj/aqua-registry-g2/")
	route := r.Method + " " + path
	switch {
	case strings.HasPrefix(route, "GET git/commits/"):
		writeJSON(w, map[string]any{"tree": map[string]string{"sha": "tree-of-" + strings.TrimPrefix(path, "git/commits/")}})
	case route == "POST git/trees":
		f.createTree(w, r)
	case route == "POST git/commits":
		var in struct {
			Tree string `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		writeJSON(w, map[string]string{"sha": "commit-of-" + in.Tree})
	case strings.HasPrefix(route, "GET git/trees/"):
		f.getTree(w, r, strings.TrimPrefix(path, "git/trees/"))
	case strings.HasPrefix(route, "GET contents/"):
		f.getContents(w, r.URL.Query().Get("ref")+":"+strings.TrimPrefix(path, "contents/"))
	case strings.HasPrefix(route, "GET git/ref/heads/"):
		w.WriteHeader(http.StatusNotFound)
	case route == "POST git/refs":
		var in struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.moved, f.movedTo = in.Ref, in.SHA
		writeJSON(w, map[string]string{})
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeGit) createTree(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseTree string                `json:"base_tree"`
		Tree     []*gogithub.TreeEntry `json:"tree"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.trees = append(f.trees, in.Tree)
	f.bases = append(f.bases, in.BaseTree)
	writeJSON(w, map[string]string{"sha": fmt.Sprintf("tree%d", len(f.trees))})
}

// getTree answers for a versions directory: its listing when asked recursively, and its sha
// otherwise.
func (f *fakeGit) getTree(w http.ResponseWriter, r *http.Request, expr string) {
	ref, _, _ := strings.Cut(expr, ":")
	if r.URL.Query().Get("recursive") != "" {
		entries := make([]map[string]string, 0, len(f.versionLists[ref]))
		for _, v := range f.versionLists[ref] {
			entries = append(entries, map[string]string{"path": v + "/registry-1.json", "type": "blob"})
		}
		writeJSON(w, map[string]any{"sha": "x", "tree": entries})
		return
	}
	sha, ok := f.versionsTrees[ref]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"sha": sha, "tree": []any{}})
}

func (f *fakeGit) getContents(w http.ResponseWriter, key string) {
	content, ok := f.files[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{
		"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newTestClient(t *testing.T, git *fakeGit) *g2.Client {
	t.Helper()
	srv := httptest.NewServer(git)
	t.Cleanup(srv.Close)
	gh, err := gogithub.NewClient(gogithub.WithURLs(new(srv.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}
	c := g2.New(gh, nil, "aquaproj", "aqua-registry-g2", "")
	c.UseIdentities(g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		testID: definition(t, "cli/cli"),
	}, nil))
	return c
}

// listIn is the versions.json the last tree wrote, read back.
func listIn(t *testing.T, git *fakeGit) *aquag2.Versions {
	t.Helper()
	last := git.trees[len(git.trees)-1]
	if len(last) != 1 || last[0].GetPath() != testDir+"/versions.json" {
		t.Fatalf("the last tree isn't the list: %+v", last)
	}
	list, err := aquag2.ReadVersions(strings.NewReader(last[0].GetContent()))
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// A commit adding a version to a package whose list is current adds the version to the list,
// in the same pull request, and records the versions directory the list is now of.
func TestClient_CommitPackage_addsToTheList(t *testing.T) {
	t.Parallel()
	held := `{"source": "versions-at-parent", "versions": [{"version": "v1.0.0", "published_at": "2026-01-01T00:00:00Z", "digest": "sha256:old"}]}`
	git := &fakeGit{
		versionsTrees: map[string]string{
			"parent":          "versions-at-parent",
			"commit-of-tree1": "versions-after",
		},
		files: map[string]string{"parent:" + testDir + "/versions.json": held},
	}
	c := newTestClient(t, git)
	content := `{"published_at":"2026-02-01T00:00:00Z","assets":[]}`
	if err := c.CommitPackage(t.Context(), slog.New(slog.DiscardHandler), "cli/cli", "ar2_"+testID, "parent", "feat: add v1.1.0", []*g2.File{
		{Path: aquag2.Path("v1.1.0"), Content: content},
	}); err != nil {
		t.Fatal(err)
	}

	if got := git.trees[0][0].GetPath(); got != testDir+"/versions/v1.1.0/registry-1.json" {
		t.Errorf("the version was written at %q", got)
	}
	list := listIn(t, git)
	if list.Source != "versions-after" {
		t.Errorf("the list is of %q", list.Source)
	}
	if diff := cmp.Diff([]string{"v1.1.0", "v1.0.0"}, list.Tags()); diff != "" {
		t.Errorf("the versions (-want +got):\n%s", diff)
	}
	if list.Versions[0].PublishedAt != "2026-02-01T00:00:00Z" || !strings.HasPrefix(list.Versions[0].Digest, "sha256:") {
		t.Errorf("the new entry: %+v", list.Versions[0])
	}
	if git.moved != "refs/heads/ar2_"+testID || git.movedTo != "commit-of-tree2" {
		t.Errorf("the branch %q was moved to %q", git.moved, git.movedTo)
	}
}

// A list that is behind its directory is made again from every version, rather than added
// to: adding to it would carry what it got wrong forward.
func TestClient_CommitPackage_rebuildsAStaleList(t *testing.T) {
	t.Parallel()
	held := `{"source": "something-else", "versions": [{"version": "v0.9.0"}]}`
	git := &fakeGit{
		versionsTrees: map[string]string{
			"parent":          "versions-at-parent",
			"commit-of-tree1": "versions-after",
		},
		versionLists: map[string][]string{"commit-of-tree1": {"v1.0.0", "v1.1.0"}},
		files: map[string]string{
			"parent:" + testDir + "/versions.json":                            held,
			"commit-of-tree1:" + testDir + "/versions/v1.0.0/registry-1.json": `{"published_at":"2026-01-01T00:00:00Z"}`,
		},
	}
	c := newTestClient(t, git)
	if err := c.CommitPackage(t.Context(), slog.New(slog.DiscardHandler), "cli/cli", "ar2_"+testID, "parent", "feat: add v1.1.0", []*g2.File{
		{Path: aquag2.Path("v1.1.0"), Content: `{"published_at":"2026-02-01T00:00:00Z"}`},
	}); err != nil {
		t.Fatal(err)
	}
	list := listIn(t, git)
	if diff := cmp.Diff([]string{"v1.1.0", "v1.0.0"}, list.Tags()); diff != "" {
		t.Errorf("the versions (-want +got):\n%s", diff)
	}
}

// A commit that changes only the definition writes no list: the versions are what they were.
func TestClient_CommitPackage_definitionOnly(t *testing.T) {
	t.Parallel()
	git := &fakeGit{}
	c := newTestClient(t, git)
	if err := c.CommitPackage(t.Context(), slog.New(slog.DiscardHandler), "cli/cli", "ar2_"+testID, "parent", "chore: tidy", []*g2.File{
		{Path: g2.ConfigFileName, Content: "name: cli/cli\n"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(git.trees) != 1 || git.trees[0][0].GetPath() != testDir+"/registry.yaml" {
		t.Errorf("trees: %+v", git.trees)
	}
}

// Taking every version out takes the list out with them.
func TestClient_CommitPackage_dropsTheList(t *testing.T) {
	t.Parallel()
	git := &fakeGit{
		versionsTrees: map[string]string{"parent": "versions-at-parent"},
		files:         map[string]string{"parent:" + testDir + "/versions.json": `{"source": "versions-at-parent", "versions": []}`},
	}
	c := newTestClient(t, git)
	if err := c.CommitPackage(t.Context(), slog.New(slog.DiscardHandler), "cli/cli", "ar2_"+testID, "parent", "fix: drop", []*g2.File{
		{Path: aquag2.Path("v1.0.0"), Deleted: true},
	}); err != nil {
		t.Fatal(err)
	}
	last := git.trees[len(git.trees)-1]
	if len(git.trees) != 2 || last[0].GetPath() != testDir+"/versions.json" || last[0].SHA != nil || last[0].Content != nil {
		t.Errorf("the list wasn't taken out: %+v", last)
	}
}

// The versions waiting for a definition are committed without the list, which the package's
// own pull requests write and would conflict with.
func TestClient_CommitWaiting(t *testing.T) {
	t.Parallel()
	git := &fakeGit{}
	c := newTestClient(t, git)
	if err := c.CommitWaiting(t.Context(), "cli/cli", "ar2_"+testID+"_v1.0.0", "parent", "feat: wait", []*g2.File{
		{Path: aquag2.Path("v1.0.0"), Content: "{}"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(git.trees) != 1 || git.trees[0][0].GetPath() != testDir+"/versions/v1.0.0/registry-1.json" {
		t.Errorf("trees: %+v", git.trees)
	}
}

// A tree of many entries is built a hundred at a time, each on top of the one before: GitHub
// times out on a request writing a thousand at once.
func TestClient_CommitEntries_incrementally(t *testing.T) {
	t.Parallel()
	git := &fakeGit{}
	c := newTestClient(t, git)
	entries := make([]*gogithub.TreeEntry, 0, 250)
	for i := range 250 {
		entries = append(entries, &gogithub.TreeEntry{
			Path: new(fmt.Sprintf("pkgs/%02d/x/registry.yaml", i%100)), Mode: new("100644"), Type: new("blob"), SHA: new("abc"),
		})
	}
	if err := c.CommitEntries(t.Context(), "ar2_consolidate", "parent", "feat: move", entries); err != nil {
		t.Fatal(err)
	}
	sizes := make([]int, 0, len(git.trees))
	for _, tree := range git.trees {
		sizes = append(sizes, len(tree))
	}
	if diff := cmp.Diff([]int{100, 100, 50}, sizes); diff != "" {
		t.Errorf("the trees' sizes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"tree-of-parent", "tree1", "tree2"}, git.bases); diff != "" {
		t.Errorf("each tree's base (-want +got):\n%s", diff)
	}
	if git.movedTo != "commit-of-tree3" {
		t.Errorf("the branch was moved to %q", git.movedTo)
	}
}
