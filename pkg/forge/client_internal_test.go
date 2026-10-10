package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gogithub "github.com/google/go-github/v92/github"
)

// The instance answers what GitHub answers, in its own words: the translation is what
// the generator reads, so this is about the fields it reads being there.
func TestClient_GetReleaseByTag(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "id": 42,
		  "tag_name": "v0.20.0",
		  "name": "v0.20.0",
		  "body": "the notes",
		  "html_url": "https://codeberg.org/mergiraf/mergiraf/releases/tag/v0.20.0",
		  "draft": false,
		  "prerelease": false,
		  "published_at": "2026-09-26T13:41:29+02:00",
		  "assets": [
		    {
		      "id": 7,
		      "name": "mergiraf_x86_64-unknown-linux-gnu.tar.gz",
		      "size": 8388608,
		      "uuid": "an-uuid",
		      "browser_download_url": "https://codeberg.org/mergiraf/mergiraf/releases/download/v0.20.0/mergiraf_x86_64-unknown-linux-gnu.tar.gz"
		    }
		  ]
		}`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	release, _, err := client.GetReleaseByTag(context.Background(), "mergiraf", "mergiraf", "v0.20.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/api/v1/repos/mergiraf/mergiraf/releases/tags/v0.20.0" {
		t.Fatalf("wanted the release the tag names, got %s", got.Path)
	}
	if release.TagName != "v0.20.0" || release.ID != 42 {
		t.Fatalf("wanted the release the instance answered with, got %+v", release)
	}
	if release.PublishedAt.IsZero() {
		t.Fatal("wanted when the release was published, which is what the generated file records")
	}
	if len(release.Assets) != 1 {
		t.Fatalf("wanted one asset, got %d", len(release.Assets))
	}
	asset := release.Assets[0]
	if asset.GetName() != "mergiraf_x86_64-unknown-linux-gnu.tar.gz" {
		t.Fatalf("wanted the asset's name, got %s", asset.GetName())
	}
	// What reads these filters on the state, which an instance doesn't say: an
	// attachment it lists is one it serves.
	if asset.GetState() != assetState {
		t.Fatalf("wanted an asset that counts as uploaded, got %q", asset.GetState())
	}
	if asset.GetBrowserDownloadURL() == "" {
		t.Fatal("wanted where the asset is downloaded from")
	}
}

// A tag holding a slash names a path the instance doesn't serve unless it is escaped.
func TestClient_GetReleaseByTag_escapedTag(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		_, _ = w.Write([]byte(`{"tag_name": "kustomize/v5.8.1"}`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	if _, _, err := client.GetReleaseByTag(context.Background(), "an-owner", "a-repo", "kustomize/v5.8.1"); err != nil {
		t.Fatal(err)
	}
	if got.EscapedPath() != "/api/v1/repos/an-owner/a-repo/releases/tags/kustomize%2Fv5.8.1" {
		t.Fatalf("wanted the tag escaped, got %s", got.EscapedPath())
	}
}

func TestClient_ListReleases(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		_, _ = w.Write([]byte(`[{"tag_name": "v0.20.0"}, {"tag_name": "v0.19.1", "prerelease": true}]`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	releases, resp, err := client.ListReleases(context.Background(), "mergiraf", "mergiraf", &gogithub.ListOptions{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 || releases[0].TagName != "v0.20.0" || !releases[1].Prerelease {
		t.Fatalf("wanted both releases as the instance said them, got %+v", releases)
	}
	// page and limit rather than per_page: this is Gitea's API rather than GitHub's.
	if q := got.Query(); q.Get("page") != "2" || q.Get("limit") != "2" {
		t.Fatalf("wanted page 2 of 2, got %s", got.RawQuery)
	}
	// A full page is followed by another, because an instance may answer with fewer
	// items than were asked for and a short page would end the list too early.
	if resp.NextPage != 3 {
		t.Fatalf("wanted another page, got %d", resp.NextPage)
	}
}

func TestClient_ListReleaseAssets(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		_, _ = w.Write([]byte(`[{"id": 1, "name": "an-asset.tar.gz"}]`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	assets, resp, err := client.ListReleaseAssets(context.Background(), "an-owner", "a-repo", 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].GetName() != "an-asset.tar.gz" {
		t.Fatalf("wanted the release's asset, got %+v", assets)
	}
	if got.Path != "/api/v1/repos/an-owner/a-repo/releases/42/assets" {
		t.Fatalf("wanted the release's assets, got %s", got.Path)
	}
	// A page shorter than what was asked for, so there is nothing after it.
	if resp.NextPage != 0 {
		t.Fatalf("wanted no further page, got %d", resp.NextPage)
	}
}

func TestClient_Get(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"description": "A syntax-aware git merge driver", "html_url": "https://codeberg.org/mergiraf/mergiraf"}`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	repo, _, err := client.Get(context.Background(), "mergiraf", "mergiraf")
	if err != nil {
		t.Fatal(err)
	}
	if repo.GetDescription() != "A syntax-aware git merge driver" {
		t.Fatalf("wanted the description the generated file carries, got %q", repo.GetDescription())
	}
}

// What the instance said about a refusal is in the error, which is where a rate limit,
// a renamed repository and a private one are told apart.
func TestClient_get_refused(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"The target couldn't be found."}`))
	}))
	defer server.Close()

	client := New(toServer(server), "codeberg.org")
	_, _, err := client.Get(context.Background(), "an-owner", "a-repo")
	if err == nil {
		t.Fatal("an error must be returned")
	}
	if !strings.Contains(err.Error(), "The target couldn't be found.") {
		t.Fatalf("wanted the error to carry what the instance said, got %v", err)
	}
}

// toServer is an HTTP client that answers from the test server whatever instance it is
// asked for, so that the request the client builds is the one under test.
func toServer(server *httptest.Server) *http.Client {
	u, err := url.Parse(server.URL)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			to := req.Clone(req.Context())
			to.URL.Scheme = u.Scheme
			to.URL.Host = u.Host
			return server.Client().Transport.RoundTrip(to)
		}),
	}
}

type roundTripper func(req *http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
