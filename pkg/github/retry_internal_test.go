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
		// retryAfter and message are what a refusal carries.
		retryAfter string
		message    string
		wantErr    bool
		wantSent   int32
	}{
		{name: "a bad minute", statuses: []int{http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusOK}, wantSent: 3},
		{name: "a bad few minutes", statuses: []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway, http.StatusOK}, wantErr: true, wantSent: queryAttempts},
		{name: "refused", statuses: []int{http.StatusUnauthorized, http.StatusOK}, wantErr: true, wantSent: 1},
		{name: "a secondary rate limit", statuses: []int{http.StatusForbidden, http.StatusOK}, retryAfter: "60", wantSent: 2},
		{name: "a secondary rate limit said only in the message", statuses: []int{http.StatusForbidden, http.StatusOK}, message: "You have exceeded a secondary rate limit.", wantSent: 2},
		{name: "too many requests", statuses: []int{http.StatusTooManyRequests, http.StatusOK}, retryAfter: "30", wantSent: 2},
		{name: "a rate limit too long to wait for", statuses: []int{http.StatusForbidden, http.StatusOK}, retryAfter: "3600", wantErr: true, wantSent: 1},
		{name: "forbidden for another reason", statuses: []int{http.StatusForbidden, http.StatusOK}, message: "Resource not accessible by integration", wantErr: true, wantSent: 1},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := d.statuses[sent.Add(1)-1]
				if status != http.StatusOK && d.retryAfter != "" {
					w.Header().Set("Retry-After", d.retryAfter)
				}
				w.WriteHeader(status)
				if status != http.StatusOK && d.message != "" {
					_, _ = w.Write([]byte(`{"message":"` + d.message + `"}`))
				}
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

// Enabling auto-merge is sent again when GitHub says something went wrong on its side, and
// not when it refuses the pull request.
func TestClient_EnableAutoMerge_retry(t *testing.T) {
	t.Parallel()
	wentWrong := `{"errors":[{"message":"Something went wrong while executing your query on 2026-11-15T04:49:27Z. Please include ` + "`ABCD`" + ` when reporting this issue."}]}`
	clean := `{"errors":[{"message":"Pull request Pull request is in clean status"}]}`
	ok := `{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`
	for _, d := range []struct {
		name     string
		answers  []string
		wantErr  bool
		wantSent int32
	}{
		{name: "a bad minute", answers: []string{wentWrong, ok}, wantSent: 2},
		{name: "a bad few minutes", answers: []string{wentWrong, wentWrong, wentWrong, ok}, wantErr: true, wantSent: queryAttempts},
		{name: "refused", answers: []string{clean, ok}, wantErr: true, wantSent: 1},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(d.answers[sent.Add(1)-1]))
			}))
			t.Cleanup(srv.Close)
			c := NewClient(srv.Client())
			c.endpoint = srv.URL
			c.retryWait = 0
			err := c.EnableAutoMerge(context.Background(), "PR_id")
			if (err != nil) != d.wantErr {
				t.Errorf("got %v, want an error: %t", err, d.wantErr)
			}
			if got := sent.Load(); got != d.wantSent {
				t.Errorf("sent %d times, want %d", got, d.wantSent)
			}
		})
	}
}

// A refusal says what GitHub answered, so that a 403 that isn't a rate limit says why.
func TestStatusError_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client())
	c.endpoint = srv.URL
	c.retryWait = 0
	err := c.post(context.Background(), "query { viewer { login } }", nil, &graphQLResponse{})
	want := `send a GraphQL request: status code 403: {"message":"Resource not accessible by integration"}`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}
