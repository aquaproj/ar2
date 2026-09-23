package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A repository an organization's IP allow list refused is read again without a token,
// and one that doesn't exist is left alone.
//
// The allow list covers public repositories whenever the request is authenticated,
// and the GitHub Actions token counts, so a run in Actions loses the star count of
// every package such an organization owns. Anonymous requests aren't covered.
func TestClient_FillForbiddenStars(t *testing.T) {
	t.Parallel()
	asked := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimPrefix(r.URL.Path, "/")
		asked[repo]++
		if r.Header.Get("Authorization") != "" {
			// The header is what the allow list rejects, so sending one would
			// defeat the point of asking again.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"stargazers_count": 1234}`)
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.anonymousEndpoint = srv.URL + "/"

	stars := map[string]int{}
	reasons := map[string]string{
		"Shopify/ejson": reasonForbidden,
		// Really gone; asking again only spends the anonymous rate limit.
		"xtaci/kcptun": "NOT_FOUND",
	}
	c.FillForbiddenStars(t.Context(), stars, reasons)

	if stars["Shopify/ejson"] != 1234 {
		t.Errorf("the refused repository has %d stars, want 1234", stars["Shopify/ejson"])
	}
	if _, ok := reasons["Shopify/ejson"]; ok {
		t.Error("a repository that was read should no longer be a failure")
	}
	if reasons["xtaci/kcptun"] != "NOT_FOUND" {
		t.Error("a repository that doesn't exist should be left alone")
	}
	if _, ok := stars["xtaci/kcptun"]; ok {
		t.Error("a repository that doesn't exist has no star count")
	}
	if asked["xtaci/kcptun"] != 0 {
		t.Errorf("asked about a repository that doesn't exist %d times", asked["xtaci/kcptun"])
	}
}

// A repository the anonymous request can't read either stays a failure, rather than
// being recorded with no stars.
func TestClient_FillForbiddenStars_refused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// What the anonymous budget running out looks like.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.anonymousEndpoint = srv.URL + "/"

	stars := map[string]int{}
	reasons := map[string]string{"Shopify/ejson": reasonForbidden}
	c.FillForbiddenStars(t.Context(), stars, reasons)

	if _, ok := stars["Shopify/ejson"]; ok {
		t.Error("a repository that couldn't be read has no star count")
	}
	if reasons["Shopify/ejson"] != reasonForbidden {
		t.Error("a repository that couldn't be read is still a failure")
	}
}
