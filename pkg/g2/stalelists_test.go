package g2_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v93/github"
)

// fakeBlobs answers for the lists, by expression.
type fakeBlobs map[string]string

func (f fakeBlobs) Blobs(_ context.Context, _ *slog.Logger, exprs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range exprs {
		if v, ok := f[e]; ok {
			out[e] = v
		}
	}
	return out, nil
}

// A list is behind when its source isn't its directory's sha, which is also a package with
// versions and no list, one with a list and no versions, and one whose list can't be read.
// Which packages there are and what each versions directory is come from one read of the
// tree under pkgs.
func TestClient_StaleVersionsLists(t *testing.T) {
	t.Parallel()
	tree := func(path, sha string) map[string]string {
		return map[string]string{"path": path, "type": "tree", "sha": sha}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/aquaproj/aqua-registry-g2/git/trees/main:pkgs" || r.URL.Query().Get("recursive") == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "pkgs", "tree": []any{
			tree("01", "s1"), tree("01/1790000001", "p1"), tree("01/1790000001/versions", "a"),
			tree("01/1790000001/versions/v1.0.0", "x"),
			map[string]string{"path": "01/1790000001/versions/v1.0.0/registry-1.json", "type": "blob", "sha": "y"},
			tree("02/1790000002", "p2"), tree("02/1790000002/versions", "b"),
			tree("03/1790000003", "p3"), tree("03/1790000003/versions", "c"),
			tree("04/1790000004", "p4"),
			tree("05/1790000005", "p5"), tree("05/1790000005/versions", "e"),
			tree("06/1790000006", "p6"),
			tree("07/not-a-package", "p7"),
		}})
	}))
	t.Cleanup(srv.Close)
	gh, err := gogithub.NewClient(gogithub.WithURLs(new(srv.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}
	list := func(id string) string { return "main:" + g2.PackageDir(id) + "/versions.json" }
	blobs := fakeBlobs{
		list("1790000001"): `{"source":"a","versions":[]}`,   // in step
		list("1790000002"): `{"source":"old","versions":[]}`, // behind
		// 1790000003 has versions and no list.
		list("1790000004"): `{"source":"d","versions":[]}`, // a list and no versions
		list("1790000005"): `{broken`,
		// 1790000006 has neither, which is in step.
	}
	client := g2.New(gh, nil, "aquaproj", "aqua-registry-g2", "")
	got, err := client.StaleVersionsLists(context.Background(), slog.New(slog.DiscardHandler), blobs, "main")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1790000002", "1790000003", "1790000004", "1790000005"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	// The lists are read from the ref given, against the default branch's directories: a
	// list the open pull request brings up to date is in step there.
	onBranch := func(id string) string { return g2.VersionsBranch + ":" + g2.PackageDir(id) + "/versions.json" }
	branchBlobs := fakeBlobs{
		onBranch("1790000001"): `{"source":"a","versions":[]}`,
		onBranch("1790000002"): `{"source":"b","versions":[]}`, // brought up to date
		onBranch("1790000003"): `{"source":"c","versions":[]}`, // brought up to date
		onBranch("1790000004"): `{"source":"d","versions":[]}`,
		onBranch("1790000005"): `{"source":"old","versions":[]}`,
	}
	got, err = client.StaleVersionsLists(context.Background(), slog.New(slog.DiscardHandler), branchBlobs, g2.VersionsBranch)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1790000004", "1790000005"}, got); diff != "" {
		t.Errorf("against the pull request's branch (-want +got):\n%s", diff)
	}
}

// The tree under pkgs is read with git from a checkout when one is named, which is how a
// registry whose tree the API can't return whole is read. The answer is the one the API
// would have given.
func TestClient_StaleVersionsLists_fromCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(p, content string) {
		t.Helper()
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pkgs/01/1790000001/registry.yaml", "name: a\n")
	write("pkgs/01/1790000001/versions/v1.0.0/registry-1.json", "{}\n")
	write("pkgs/02/1790000002/registry.yaml", "name: b\n")
	write("pkgs/02/1790000002/versions/v2.0.0/registry-1.json", "{}\n")
	write("pkgs/03/1790000003/registry.yaml", "name: c\n")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...) //nolint:gosec // the test's own arguments
		// A git hook runs the tests with GIT_DIR and GIT_INDEX_FILE pointing at the
		// repository being committed to, and they win over -C: the commit below landed
		// there, replacing its tree with this one.
		cmd.Env = withoutGitEnv(os.Environ())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	versionsSHA := func(id, shard string) string { return git("rev-parse", "HEAD:pkgs/"+shard+"/"+id+"/versions") }

	list := func(id string) string { return "main:" + g2.PackageDir(id) + "/versions.json" }
	blobs := fakeBlobs{
		list("1790000001"): `{"source":"` + versionsSHA("1790000001", "01") + `","versions":[]}`, // in step
		list("1790000002"): `{"source":"old","versions":[]}`,                                     // behind
		// 1790000003 has neither versions nor a list, which is in step.
	}
	client := g2.New(nil, nil, "aquaproj", "aqua-registry-g2", "")
	client.ReadTreesFrom(dir)
	got, err := client.StaleVersionsLists(context.Background(), slog.New(slog.DiscardHandler), blobs, "main")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1790000002"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// withoutGitEnv drops the variables a git hook sets for the repository it runs in.
func withoutGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
