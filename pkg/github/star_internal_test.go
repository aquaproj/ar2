package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBuildQuery(t *testing.T) {
	t.Parallel()
	query, vars := buildQuery([]Repo{
		{Owner: "cli", Name: "cli"},
		{Owner: "aquaproj", Name: "aqua"},
	})
	for _, s := range []string{
		"query($r0o: String!, $r0n: String!, $r1o: String!, $r1n: String!)",
		"r0: repository(owner: $r0o, name: $r0n) { stargazerCount }",
		"r1: repository(owner: $r1o, name: $r1n) { stargazerCount }",
	} {
		if !strings.Contains(query, s) {
			t.Errorf("query doesn't contain %q\nquery: %s", s, query)
		}
	}
	want := map[string]any{"r0o": "cli", "r0n": "cli", "r1o": "aquaproj", "r1n": "aqua"}
	if diff := cmp.Diff(want, vars); diff != "" {
		t.Errorf("variables are wrong (-want +got):\n%s", diff)
	}
}

func TestLimitedAliases(t *testing.T) {
	t.Parallel()
	got := limitedAliases([]graphQLError{
		{Type: "NOT_FOUND", Path: []any{"r0"}},
		{Type: errResourceLimits, Path: []any{"r1", "stargazerCount"}},
		{Type: errResourceLimits},
	})
	want := map[string]struct{}{"r1": {}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("limitedAliases is wrong (-want +got):\n%s", diff)
	}
}

// TestClient_GetStars_resourceLimits checks that repositories dropped with
// RESOURCE_LIMITS_EXCEEDED are retried instead of silently missing from the result,
// which is what made an early run lose the star count of 87 packages.
func TestClient_GetStars_resourceLimits(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			// r0 answered, r1 over the limit, r2 missing.
			_, _ = w.Write([]byte(`{"data":{"r0":{"stargazerCount":10},"r1":null,"r2":null},
				"errors":[{"type":"RESOURCE_LIMITS_EXCEEDED","path":["r1","stargazerCount"]},
				          {"type":"NOT_FOUND","path":["r2"]}]}`))
			return
		}
		// The retry asks only for the repository that hit the limit.
		if len(req.Variables) != 2 {
			t.Errorf("the retry should ask for 1 repository, got %d variables", len(req.Variables))
		}
		_, _ = w.Write([]byte(`{"data":{"r0":{"stargazerCount":20}}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	c.endpoint = srv.URL
	got, reasons, err := c.GetStars(t.Context(), []Repo{
		{Owner: "o", Name: "answered"},
		{Owner: "o", Name: "limited"},
		{Owner: "o", Name: "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"o/answered": 10, "o/limited": 20}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("stars are wrong (-want +got):\n%s", diff)
	}
	if calls != 2 {
		t.Errorf("the client should retry once, made %d requests", calls)
	}
	// The repository that wasn't answered carries why, so the caller can tell a
	// deleted repository from an organization refusing the request.
	if got := reasons["o/missing"]; got != "NOT_FOUND" {
		t.Errorf("the reason is %q, want NOT_FOUND", got)
	}
}
