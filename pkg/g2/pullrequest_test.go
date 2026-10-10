package g2_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// The pull request waiting for a definition is found by the package's name, which the
// client resolves to the id its version branches are named after.
func TestWaitingPullRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]*gogithub.PullRequest{
			{Number: new(1), Head: &gogithub.PullRequestBranch{Ref: new("ar2_1790772767")}},
			{Number: new(2), Head: &gogithub.PullRequestBranch{Ref: new("ar2_1790772767_v2.0.0")}},
		})
	}))
	t.Cleanup(srv.Close)
	gh, err := gogithub.NewClient(gogithub.WithURLs(new(srv.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}

	c := g2.New(gh, nil, "aquaproj", "aqua-registry-g2", "")
	c.UseIdentities(g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		"1790772767": definition(t, "cli/cli"),
	}, nil))

	pr, err := c.WaitingPullRequest(context.Background(), "cli/cli")
	if err != nil {
		t.Fatal(err)
	}
	if pr.GetNumber() != 2 {
		t.Errorf("found pull request %d, want 2", pr.GetNumber())
	}
}
