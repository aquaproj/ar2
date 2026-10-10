package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gogithub "github.com/google/go-github/v92/github"
)

// gitLabMaxPerPage is the largest page GitLab answers with.
const gitLabMaxPerPage = 100

// errNoRelease is returned when a project has published none.
var errNoRelease = errors.New("the project has no published release")

// GitLab reads a GitLab instance the way aqua gr reads GitHub.
//
// GitLab's API is its own rather than Gitea's with another name on it: it is /api/v4, a
// project is one escaped path rather than an owner and a name, a page is asked for with
// page and per_page, and a release says released_at and upcoming_release where the others
// say published_at, draft and prerelease. The answers are translated into go-github's
// types for the same reason the Gitea client's are: what reads them is aqua's generator,
// so the asset naming of a release on an instance is inferred by the code that infers
// GitHub's.
//
// Two things about an asset are GitLab's alone, and both are in servedAsset.
type GitLab struct {
	httpClient *http.Client
	host       string
	// assets are the assets of each release this client has read, by the id it gave
	// them. GitLab has no id of its own for a release, and the generator asks for a
	// release's assets by one; the assets come with the release, so what is asked for
	// is already here.
	mu     sync.Mutex
	assets map[int64][]*gogithub.ReleaseAsset
	lastID int64
}

// NewGitLab returns a client reading the GitLab instance at the given host.
func NewGitLab(httpClient *http.Client, host string) *GitLab {
	return &GitLab{
		httpClient: httpClient,
		host:       host,
		assets:     map[int64][]*gogithub.ReleaseAsset{},
	}
}

// Host is the instance the client reads.
func (c *GitLab) Host() string {
	return c.host
}

// Get returns what the generator reads about a project, which is its description.
func (c *GitLab) Get(ctx context.Context, owner, repo string) (*gogithub.Repository, *gogithub.Response, error) {
	out := &gitLabProject{}
	if err := c.get(ctx, c.path(owner, repo), out); err != nil {
		return nil, nil, err
	}
	return &gogithub.Repository{
		Description: &out.Description,
		HTMLURL:     &out.WebURL,
	}, &gogithub.Response{}, nil
}

// GetLatestRelease returns the instance's newest release of the project.
//
// GitLab answers with the releases newest first and has no endpoint for the newest one,
// so it is the first of them that is published: a release dated in the future publishes
// nothing to install yet.
func (c *GitLab) GetLatestRelease(ctx context.Context, owner, repo string) (*gogithub.RepositoryRelease, *gogithub.Response, error) {
	releases, resp, err := c.ListReleases(ctx, owner, repo, &gogithub.ListOptions{PerPage: 1})
	if err != nil {
		return nil, nil, err
	}
	for _, release := range releases {
		if release.Draft {
			continue
		}
		return release, resp, nil
	}
	return nil, nil, errNoRelease
}

// GetReleaseByTag returns the release the tag names.
func (c *GitLab) GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*gogithub.RepositoryRelease, *gogithub.Response, error) {
	out := &gitLabRelease{}
	if err := c.get(ctx, c.path(owner, repo)+"/releases/"+url.PathEscape(tag), out); err != nil {
		return nil, nil, err
	}
	return c.release(out), &gogithub.Response{}, nil
}

// ListReleaseAssets returns one page of a release's assets.
//
// The assets came with the release, so this answers from what reading it kept. An id this
// client never gave out has no assets, which is what an empty page says.
func (c *GitLab) ListReleaseAssets(_ context.Context, _, _ string, id int64, opts *gogithub.ListOptions) ([]*gogithub.ReleaseAsset, *gogithub.Response, error) {
	c.mu.Lock()
	assets := c.assets[id]
	c.mu.Unlock()
	page, limit := gitLabPaging(opts)
	from := (page - 1) * limit
	if from >= len(assets) {
		return nil, &gogithub.Response{}, nil
	}
	to := min(from+limit, len(assets))
	return assets[from:to], response(page, limit, to-from), nil
}

// ListReleases returns one page of the project's releases, newest first.
func (c *GitLab) ListReleases(ctx context.Context, owner, repo string, opts *gogithub.ListOptions) ([]*gogithub.RepositoryRelease, *gogithub.Response, error) {
	out := []*gitLabRelease{}
	page, limit := gitLabPaging(opts)
	if err := c.get(ctx, fmt.Sprintf("%s/releases?page=%d&per_page=%d", c.path(owner, repo), page, limit), &out); err != nil {
		return nil, nil, err
	}
	releases := make([]*gogithub.RepositoryRelease, 0, len(out))
	for _, r := range out {
		releases = append(releases, c.release(r))
	}
	return releases, response(page, limit, len(out)), nil
}

// path is where the instance serves a project.
//
// The project is <namespace>/<project>, which GitLab reads as one id: the separators
// inside it are escaped rather than kept, and a project inside subgroups is named the
// same way.
func (c *GitLab) path(owner, repo string) string {
	return fmt.Sprintf("https://%s/api/v4/projects/%s", c.host, url.PathEscape(owner+"/"+repo))
}

// get reads one answer as JSON.
func (c *GitLab) get(ctx context.Context, endpoint string, out any) error {
	return get(ctx, c.httpClient, endpoint, out)
}

// release translates a release into what the generator reads, keeping its assets to
// answer for the id this gives it.
func (c *GitLab) release(r *gitLabRelease) *gogithub.RepositoryRelease {
	assets := make([]*gogithub.ReleaseAsset, 0, len(r.Assets.Links))
	for _, link := range r.Assets.Links {
		name, ok := servedAsset(c.host, link.URL, link.DirectAssetURL)
		if !ok {
			continue
		}
		state := assetState
		assets = append(assets, &gogithub.ReleaseAsset{
			Name:  &name,
			State: &state,
			// Where the instance serves it, which is the permanent release
			// link rather than wherever the file was uploaded.
			BrowserDownloadURL: &link.DirectAssetURL,
			CreatedAt:          &gogithub.Timestamp{Time: r.ReleasedAt},
		})
	}

	c.mu.Lock()
	c.lastID++
	id := c.lastID
	c.assets[id] = assets
	c.mu.Unlock()

	return &gogithub.RepositoryRelease{
		ID:      id,
		TagName: r.TagName,
		Name:    &r.Name,
		Body:    &r.Description,
		HTMLURL: r.Links.Self,
		// GitLab has no draft and no prerelease. A release dated in the future is
		// the same answer to the same question: what it names isn't one to install
		// from yet.
		Draft:       r.UpcomingRelease,
		PublishedAt: &gogithub.Timestamp{Time: r.ReleasedAt},
		Assets:      assets,
	}
}

// gitLabProject is as much of a project as the generator reads.
type gitLabProject struct {
	Description string `json:"description"`
	WebURL      string `json:"web_url"`
}

// gitLabRelease is a release as GitLab answers with it.
type gitLabRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Assets      struct {
		// Links are the files the release publishes. GitLab has sources beside
		// them -- the archives it makes of the tag -- which are not what a package
		// is installed from.
		Links []struct {
			Name string `json:"name"`
			// URL is where the file itself is, which is not always the instance:
			// a release link can point anywhere.
			URL string `json:"url"`
			// DirectAssetURL is where the instance serves the file. It is the
			// permanent release link when the link was created with a file path,
			// and wherever the file was uploaded when it wasn't.
			DirectAssetURL string `json:"direct_asset_url"`
		} `json:"links"`
	} `json:"assets"`
	ReleasedAt      time.Time `json:"released_at"`
	UpcomingRelease bool      `json:"upcoming_release"`
	Links           struct {
		Self string `json:"self"`
	} `json:"_links"`
}

// servedAsset is the name the instance serves an asset by, and whether it serves it at all.
//
// Two things have to be true, and the API answers both.
//
// The link has a permanent URL, /-/releases/<tag>/downloads/<path>, which GitLab builds
// from the file path the link was created with. That path is what the file is served at,
// and the API gives it only inside this URL: the name beside it is a label -- "package:
// RPM riscv64" is one -- and the two are the same string only where whoever published the
// release made them so. The path arrives escaped, because it arrives inside a URL, and
// what names the asset is the path itself.
//
// And the file is on the instance, which is where a link pointing anywhere else fails:
// gitlab-org/gitlab-runner publishes to S3, and asking the permanent link for one of
// those assets answers with the page GitLab shows before sending a reader to another
// site. A few hundred bytes of HTML is not a refusal a download notices, so such an asset
// is left out here.
func servedAsset(host, assetURL, directAssetURL string) (string, bool) {
	u, err := url.Parse(assetURL)
	if err != nil || u.Host != host {
		return "", false
	}
	_, afterRelease, found := strings.Cut(directAssetURL, "/-/releases/")
	if !found {
		return "", false
	}
	_, escaped, found := strings.Cut(afterRelease, "/downloads/")
	if !found || escaped == "" {
		return "", false
	}
	name, err := url.PathUnescape(escaped)
	if err != nil || name == "" {
		return "", false
	}
	return name, true
}

// gitLabPaging is the page and the page size to ask for, from what a GitHub caller asked
// for.
func gitLabPaging(opts *gogithub.ListOptions) (int, int) {
	page, limit := 1, gitLabMaxPerPage
	if opts != nil {
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.PerPage > 0 && opts.PerPage < gitLabMaxPerPage {
			limit = opts.PerPage
		}
	}
	return page, limit
}
