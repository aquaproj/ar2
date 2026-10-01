package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The prefix is a prefix, not a search. GitHub's refs(query:) answers with every branch
// whose name holds the string anywhere, so asking for the package branches also returns
// the head branch of a pull request for a package whose name holds it -- yarnpkg/berry's
// is ar2_yarnpkg_2fberry. Writing to one of those would write to a pull request.
func TestBranches_Files_prefix(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data": {"repository": {"refs": {
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [
				{"name": "pkg_1790772767", "target": {"file": {"object": {"text": "name: cli/cli\n"}}}},
				{"name": "ar2_yarnpkg_2fberry", "target": {"file": {"object": {"text": "name: yarnpkg/berry\n"}}}}
			]
		}}}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	c.endpoint = srv.URL
	logger := slog.New(slog.DiscardHandler)

	files, err := c.Branches("aquaproj", "aqua-registry-g2").Files(context.Background(), logger, "pkg_", "registry.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"pkg_1790772767": "name: cli/cli\n"}
	if diff := cmp.Diff(want, files); diff != "" {
		t.Error(diff)
	}
}
