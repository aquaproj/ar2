package g2_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v93/github"
)

// refsServer answers for a head branch that exists, with or without a pull request open on
// it, and records what was done to the branch.
type refsServer struct {
	mu    sync.Mutex
	open  bool
	calls []string
}

func (s *refsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/repos/aquaproj/aqua-registry-g2/")
	switch route := r.Method + " " + path; {
	case strings.HasPrefix(route, "GET git/ref/heads/"):
		writeJSON(w, map[string]any{"object": map[string]string{"sha": "old"}})
	case route == "GET pulls":
		if s.open {
			writeJSON(w, []map[string]any{{"number": 1}})
			return
		}
		writeJSON(w, []any{})
	default:
		s.calls = append(s.calls, route)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, map[string]any{})
	}
}

// A head branch no pull request is open on is deleted and created again rather than moved:
// moving one far behind the default branch is what GitHub times out on. One a pull request
// is open on is moved, since deleting it would close the pull request.
func TestClient_MoveBranch_leftover(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		name string
		open bool
		want []string
	}{
		{name: "left over", want: []string{"DELETE git/refs/heads/ar2_1790815710", "POST git/refs"}},
		{name: "a pull request is open", open: true, want: []string{"PATCH git/refs/heads/ar2_1790815710"}},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			s := &refsServer{open: d.open}
			srv := httptest.NewServer(s)
			t.Cleanup(srv.Close)
			gh, err := gogithub.NewClient(gogithub.WithURLs(new(srv.URL+"/"), nil))
			if err != nil {
				t.Fatal(err)
			}
			c := g2.New(gh, nil, "aquaproj", "aqua-registry-g2", "")
			if err := c.MoveBranch(context.Background(), "ar2_1790815710", "new"); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(d.want, s.calls); diff != "" {
				t.Errorf("what was done to the branch (-want +got):\n%s", diff)
			}
		})
	}
}
