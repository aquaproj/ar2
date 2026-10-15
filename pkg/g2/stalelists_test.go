package g2_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
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
	got, err := g2.New(gh, nil, "aquaproj", "aqua-registry-g2", "").StaleVersionsLists(context.Background(), slog.New(slog.DiscardHandler), blobs)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1790000002", "1790000003", "1790000004", "1790000005"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
