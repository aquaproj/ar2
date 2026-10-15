package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A query GitHub answers with a 502 or a 504 is sent again, and one it refuses for another
// reason isn't.
func TestClient_post_retry(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		name     string
		statuses []int
		wantErr  bool
		wantSent int32
	}{
		{name: "a bad minute", statuses: []int{http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusOK}, wantSent: 3},
		{name: "a bad few minutes", statuses: []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway, http.StatusOK}, wantErr: true, wantSent: queryAttempts},
		{name: "refused", statuses: []int{http.StatusUnauthorized, http.StatusOK}, wantErr: true, wantSent: 1},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := d.statuses[sent.Add(1)-1]
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"data":{}}`))
				}
			}))
			t.Cleanup(srv.Close)
			c := NewClient(srv.Client())
			c.endpoint = srv.URL
			c.retryWait = 0
			err := c.post(context.Background(), "query { viewer { login } }", nil, &graphQLResponse{})
			if (err != nil) != d.wantErr {
				t.Errorf("got %v, want an error: %t", err, d.wantErr)
			}
			if got := sent.Load(); got != d.wantSent {
				t.Errorf("sent %d times, want %d", got, d.wantSent)
			}
		})
	}
}
