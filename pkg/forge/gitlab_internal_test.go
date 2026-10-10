package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

// GitLab answers what the generator reads in its own words, and an asset is the one thing
// it doesn't answer directly: what is served is the file path the release link was created
// with, which the API gives only inside the permanent link.
func TestGitLab_GetReleaseByTag(t *testing.T) { //nolint:funlen
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "tag_name": "v1.122.0",
		  "name": "v1.122.0",
		  "description": "the notes",
		  "released_at": "2026-09-26T13:41:29.000Z",
		  "upcoming_release": false,
		  "_links": {"self": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0"},
		  "assets": {
		    "count": 4,
		    "sources": [{"format": "zip", "url": "https://gitlab.com/gitlab-org/cli/-/archive/v1.122.0/cli.zip"}],
		    "links": [
		      {
		        "name": "glab_1.122.0_darwin_arm64.tar.gz",
		        "url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/glab_darwin_arm64.tar.gz",
		        "direct_asset_url": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0/downloads/glab_1.122.0_darwin_arm64.tar.gz"
		      },
		      {
		        "name": "package: RPM riscv64",
		        "url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/glab.rpm",
		        "direct_asset_url": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0/downloads/packages/rpm/glab%20riscv64.rpm"
		      },
		      {
		        "name": "glab_1.122.0_linux_s390x.rpm",
		        "url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/glab.rpm",
		        "direct_asset_url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/glab.rpm"
		      },
		      {
		        "name": "binary: macOS arm64",
		        "url": "https://downloads.example.com/v1.122.0/binaries/glab-darwin-arm64",
		        "direct_asset_url": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0/downloads/binaries/glab-darwin-arm64"
		      }
		    ]
		  }
		}`))
	}))
	defer server.Close()

	client := NewGitLab(toServer(server), "gitlab.com")
	release, _, err := client.GetReleaseByTag(context.Background(), "gitlab-org", "cli", "v1.122.0")
	if err != nil {
		t.Fatal(err)
	}
	// The project is one escaped id, which is also how a project inside subgroups is
	// named.
	if got.EscapedPath() != "/api/v4/projects/gitlab-org%2Fcli/releases/v1.122.0" {
		t.Fatalf("wanted the release the tag names, got %s", got.EscapedPath())
	}
	if release.TagName != "v1.122.0" {
		t.Fatalf("wanted the release the instance answered with, got %+v", release)
	}
	// released_at is what GitLab calls it, and when a release was published is what
	// the generated file records.
	if release.PublishedAt.IsZero() {
		t.Fatal("wanted when the release was published")
	}
	// The path the permanent link serves the file at, rather than the name beside it:
	// the second link is named "package: RPM riscv64", and what it serves is a path
	// that holds a space -- escaped in the link, and not in what names the asset.
	//
	// Two are left out. The third has no permanent link -- GitLab answered with where
	// the file was uploaded -- so there is no path to install it by. The fourth has
	// one, but the file is on another host, and asking the permanent link for it
	// answers with the page GitLab shows before sending a reader to another site.
	want := []string{"glab_1.122.0_darwin_arm64.tar.gz", "packages/rpm/glab riscv64.rpm"}
	if diff := cmp.Diff(want, names(t, release.Assets)); diff != "" {
		t.Fatalf("the assets the instance serves are wrong (-want +got):\n%s", diff)
	}

	// GitLab has no id for a release and the generator asks for a release's assets by
	// one, so the client gives it an id and answers for that.
	assets, _, err := client.ListReleaseAssets(context.Background(), "gitlab-org", "cli", release.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, names(t, assets)); diff != "" {
		t.Fatalf("the release's assets are wrong (-want +got):\n%s", diff)
	}
	// An id from nowhere is a release this client never read.
	other, _, err := client.ListReleaseAssets(context.Background(), "gitlab-org", "cli", release.ID+1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("wanted no asset for an id that was never given out, got %d", len(other))
	}
}

// names are the assets' names, with what every one of them has to say checked on the way:
// the state what reads them filters on, and where the instance serves the file.
func names(t *testing.T, assets []*gogithub.ReleaseAsset) []string {
	t.Helper()
	out := make([]string, 0, len(assets))
	for _, asset := range assets {
		if asset.GetState() != assetState {
			t.Fatalf("wanted an asset that counts as uploaded, got %q", asset.GetState())
		}
		if asset.GetBrowserDownloadURL() == "" {
			t.Fatal("wanted where the asset is downloaded from")
		}
		out = append(out, asset.GetName())
	}
	return out
}

// A release dated in the future publishes nothing to install yet, which is what GitLab
// says instead of draft.
func TestGitLab_GetLatestRelease(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		  {"tag_name": "v2.0.0", "upcoming_release": true, "released_at": "2027-01-01T00:00:00.000Z"},
		  {"tag_name": "v1.122.0", "upcoming_release": false, "released_at": "2026-09-26T13:41:29.000Z"}
		]`))
	}))
	defer server.Close()

	release, _, err := NewGitLab(toServer(server), "gitlab.com").
		GetLatestRelease(context.Background(), "gitlab-org", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if got.EscapedPath() != "/api/v4/projects/gitlab-org%2Fcli/releases" {
		t.Fatalf("wanted the project's releases, got %s", got.EscapedPath())
	}
	if release.TagName != "v1.122.0" {
		t.Fatalf("wanted the newest published release, got %s", release.TagName)
	}
}

// A page is asked for with page and per_page, and GitLab's largest is a hundred.
func TestGitLab_ListReleases_paging(t *testing.T) {
	t.Parallel()
	var got *url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := NewGitLab(toServer(server), "gitlab.com")
	if _, _, err := client.ListReleases(context.Background(), "gitlab-org", "cli", nil); err != nil {
		t.Fatal(err)
	}
	if got.Query().Get("page") != "1" || got.Query().Get("per_page") != "100" {
		t.Fatalf("wanted the first page of GitLab's largest, got %s", got.RawQuery)
	}
	if _, _, err := client.ListReleases(context.Background(), "gitlab-org", "cli",
		&gogithub.ListOptions{Page: 3, PerPage: 20}); err != nil {
		t.Fatal(err)
	}
	if got.Query().Get("page") != "3" || got.Query().Get("per_page") != "20" {
		t.Fatalf("wanted the page that was asked for, got %s", got.RawQuery)
	}
}
