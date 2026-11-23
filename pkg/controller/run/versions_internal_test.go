package run

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aquaproj/ar2/pkg/state"
	gogithub "github.com/google/go-github/v93/github"
)

// releasesServer serves a repository's releases a page at a time, the way GitHub does:
// the Link header names the next page until the last.
func releasesServer(t *testing.T, total int) (*gogithub.Client, *int) {
	t.Helper()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * versionsPerPage
		end := min(start+versionsPerPage, total)
		releases := make([]map[string]string, 0, versionsPerPage)
		for i := start; i < end; i++ {
			releases = append(releases, map[string]string{"tag_name": fmt.Sprintf("v0.%d.0", total-i)})
		}
		if end < total {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d&per_page=%d>; rel="next"`,
				"http://"+r.Host, r.URL.Path, page+1, versionsPerPage))
		}
		if err := json.NewEncoder(w).Encode(releases); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	gh, err := gogithub.NewClient(gogithub.WithEnterpriseURLs(srv.URL, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return gh, &requests
}

// A package read one page deep gets the newest hundred releases, and a named one is read
// until the releases end or namedPages pages have been read.
func TestReleaseVersions_pages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		total, pages int
		want         int
		wantRequests int
	}{
		{name: "the order reads one page", total: 250, pages: 1, want: 100, wantRequests: 1},
		{name: "a named package reads until the releases end", total: 250, pages: namedPages, want: 250, wantRequests: 3},
		{name: "a named package reads no more than namedPages", total: 1500, pages: namedPages, want: 1000, wantRequests: namedPages},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh, requests := releasesServer(t, tt.total)
			c := New(gh, nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
			got, err := c.releaseVersions(t.Context(), &state.Package{RepoOwner: "loft-sh", RepoName: "vcluster"}, nil, false, tt.pages)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d versions, want %d", len(got), tt.want)
			}
			if *requests != tt.wantRequests {
				t.Errorf("made %d requests, want %d", *requests, tt.wantRequests)
			}
		})
	}
}
