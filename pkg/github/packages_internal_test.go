package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testRepository(t *testing.T, handler http.HandlerFunc) *Repository {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client())
	c.endpoint = srv.URL
	return c.Repository("aquaproj", "aqua-registry-g2")
}

// Only directories two levels down are packages. A file beside them -- a README, say -- is
// not one.
func TestRepository_Subtrees(t *testing.T) {
	t.Parallel()
	r := testRepository(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data": {"repository": {"object": {"entries": [
			{"name": "67", "type": "tree", "object": {"entries": [
				{"name": "1790772767", "type": "tree"},
				{"name": "README.md", "type": "blob"}
			]}},
			{"name": "README.md", "type": "blob", "object": null}
		]}}}}`)
	})
	got, err := r.Subtrees(context.Background(), "main:pkgs")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"67/1790772767"}, got); diff != "" {
		t.Error(diff)
	}
}

// A registry no package has been moved into yet has no pkgs directory at all.
func TestRepository_Subtrees_none(t *testing.T) {
	t.Parallel()
	r := testRepository(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data": {"repository": {"object": null}}}`)
	})
	got, err := r.Subtrees(context.Background(), "main:pkgs")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// Each expression is an alias of its own. One naming nothing is left out, and one GitHub
// wouldn't send whole is too, rather than read as half a file.
func TestRepository_Blobs(t *testing.T) {
	t.Parallel()
	var query string
	r := testRepository(t, func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var in struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(body, &in)
		query = in.Query
		fmt.Fprint(w, `{"data": {"repository": {
			"f0": {"text": "name: cli/cli\n", "isTruncated": false},
			"f1": null,
			"f2": {"text": "name: big", "isTruncated": true}
		}}}`)
	})
	exprs := []string{
		"main:pkgs/67/1790772767/registry.yaml",
		"main:pkgs/68/1790772768/registry.yaml",
		`main:pkgs/69/1790772769/registry.yaml`,
	}
	got, err := r.Blobs(context.Background(), slog.New(slog.DiscardHandler), exprs)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]string{exprs[0]: "name: cli/cli\n"}, got); diff != "" {
		t.Error(diff)
	}
	if !strings.Contains(query, `f0: object(expression: "main:pkgs/67/1790772767/registry.yaml")`) {
		t.Errorf("the query doesn't ask for the first expression under its alias:\n%s", query)
	}
}

// An error that isn't about one alias means the answer can't be used, and a page silently
// missing would look like packages that aren't there.
func TestRepository_Blobs_error(t *testing.T) {
	t.Parallel()
	r := testRepository(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data": null, "errors": [{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}]}`)
	})
	if _, err := r.Blobs(context.Background(), slog.New(slog.DiscardHandler), []string{"main:x"}); err == nil {
		t.Fatal("a rate limit was read as an answer")
	}
}

func TestRepository_OpenPullRequestHeads(t *testing.T) {
	t.Parallel()
	page := 0
	r := testRepository(t, func(w http.ResponseWriter, _ *http.Request) {
		page++
		if page == 1 {
			fmt.Fprint(w, `{"data": {"repository": {"pullRequests": {
				"pageInfo": {"hasNextPage": true, "endCursor": "c1"},
				"nodes": [{"headRefName": "ar2_1790772767"}]
			}}}}`)
			return
		}
		fmt.Fprint(w, `{"data": {"repository": {"pullRequests": {
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [{"headRefName": "ar2_index"}]
		}}}}`)
	})
	got, err := r.OpenPullRequestHeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"ar2_1790772767", "ar2_index"}, got); diff != "" {
		t.Error(diff)
	}
}
