// Package forge reads a forge instance the way aqua gr reads GitHub.
//
// Both serve the API Gitea wrote and Forgejo inherited, at /api/v1, which GitHub's was
// the model for: a release says tag_name, published_at, draft and prerelease, and an
// asset says name and browser_download_url. What differs is how a page is asked for --
// page and limit rather than per_page -- and that nothing says how many pages there are.
//
// The answers are translated into go-github's types, because what reads them is aqua's
// generator, which is written against GitHub. Translating here is what lets the asset
// naming of a release on an instance be inferred by the same code that infers GitHub's.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// assetState is what the translated assets say.
//
// GitHub keeps an asset whose upload never completed in the state "starter", and what
// reads these filters on the state being "uploaded". An instance serves no such thing:
// an attachment it lists is one it serves, so saying "uploaded" is saying what is true
// rather than pretending.
const assetState = "uploaded"

// Client reads one instance. Which instance is not an argument because a package is on
// one of them: a client is made for the package's own.
type Client struct {
	httpClient *http.Client
	host       string
}

// New returns a client reading the instance at the given host.
func New(httpClient *http.Client, host string) *Client {
	return &Client{httpClient: httpClient, host: host}
}

// Host is the instance the client reads.
func (c *Client) Host() string {
	return c.host
}

// Get returns what the generator reads about a repository, which is its description.
func (c *Client) Get(ctx context.Context, owner, repo string) (*gogithub.Repository, *gogithub.Response, error) {
	out := &repository{}
	if err := c.get(ctx, c.path(owner, repo), out); err != nil {
		return nil, nil, err
	}
	return &gogithub.Repository{
		Description: &out.Description,
		HTMLURL:     &out.HTMLURL,
	}, &gogithub.Response{}, nil
}

// GetLatestRelease returns the instance's newest release of the repository.
func (c *Client) GetLatestRelease(ctx context.Context, owner, repo string) (*gogithub.RepositoryRelease, *gogithub.Response, error) {
	out := &release{}
	if err := c.get(ctx, c.path(owner, repo)+"/releases/latest", out); err != nil {
		return nil, nil, err
	}
	return out.release(), &gogithub.Response{}, nil
}

// GetReleaseByTag returns the release the tag names.
func (c *Client) GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*gogithub.RepositoryRelease, *gogithub.Response, error) {
	out := &release{}
	if err := c.get(ctx, c.path(owner, repo)+"/releases/tags/"+url.PathEscape(tag), out); err != nil {
		return nil, nil, err
	}
	return out.release(), &gogithub.Response{}, nil
}

// ListReleaseAssets returns one page of a release's assets.
func (c *Client) ListReleaseAssets(ctx context.Context, owner, repo string, id int64, opts *gogithub.ListOptions) ([]*gogithub.ReleaseAsset, *gogithub.Response, error) {
	out := []*asset{}
	page, limit := paging(opts)
	if err := c.get(ctx, fmt.Sprintf("%s/releases/%d/assets?page=%d&limit=%d", c.path(owner, repo), id, page, limit), &out); err != nil {
		return nil, nil, err
	}
	assets := make([]*gogithub.ReleaseAsset, 0, len(out))
	for _, a := range out {
		assets = append(assets, a.asset())
	}
	return assets, response(page, limit, len(out)), nil
}

// ListReleases returns one page of the repository's releases, newest first.
func (c *Client) ListReleases(ctx context.Context, owner, repo string, opts *gogithub.ListOptions) ([]*gogithub.RepositoryRelease, *gogithub.Response, error) {
	out := []*release{}
	page, limit := paging(opts)
	if err := c.get(ctx, fmt.Sprintf("%s/releases?page=%d&limit=%d", c.path(owner, repo), page, limit), &out); err != nil {
		return nil, nil, err
	}
	releases := make([]*gogithub.RepositoryRelease, 0, len(out))
	for _, r := range out {
		releases = append(releases, r.release())
	}
	return releases, response(page, limit, len(out)), nil
}

// path is where the instance serves a repository.
func (c *Client) path(owner, repo string) string {
	return fmt.Sprintf("https://%s/api/v1/repos/%s/%s", c.host, url.PathEscape(owner), url.PathEscape(repo))
}

// get reads one answer as JSON.
func (c *Client) get(ctx context.Context, endpoint string, out any) error {
	return get(ctx, c.httpClient, endpoint, out)
}

// get reads one answer as JSON, which every instance here is read the same way: a GET
// that accepts JSON, a status code to refuse on, and a body to decode.
func get(ctx context.Context, httpClient *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create a request for the instance: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send a request to the instance: %w", slogerr.With(err, "api_endpoint", endpoint))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read the instance's answer: %w", slogerr.With(err, "api_endpoint", endpoint))
	}
	if resp.StatusCode != http.StatusOK {
		// With what the instance said about it, which is where a rate limit, a
		// renamed repository and a private one are told apart.
		return slogerr.With(fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, said(b)), //nolint:wrapcheck
			"api_endpoint", endpoint)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode the instance's answer as JSON: %w", slogerr.With(err, "api_endpoint", endpoint))
	}
	return nil
}

// repository is as much of a repository as the generator reads.
type repository struct {
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
}

// release is a release as the instance answers with it.
type release struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []*asset  `json:"assets"`
}

// release translates it into what the generator reads.
func (r *release) release() *gogithub.RepositoryRelease {
	out := &gogithub.RepositoryRelease{
		ID:          r.ID,
		TagName:     r.TagName,
		Name:        &r.Name,
		Body:        &r.Body,
		HTMLURL:     r.HTMLURL,
		Draft:       r.Draft,
		Prerelease:  r.Prerelease,
		PublishedAt: &gogithub.Timestamp{Time: r.PublishedAt},
		Assets:      make([]*gogithub.ReleaseAsset, 0, len(r.Assets)),
	}
	for _, a := range r.Assets {
		out.Assets = append(out.Assets, a.asset())
	}
	return out
}

// asset is a release asset as the instance answers with it.
type asset struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Size               int64     `json:"size"`
	BrowserDownloadURL string    `json:"browser_download_url"`
	CreatedAt          time.Time `json:"created_at"`
}

// asset translates it into what the generator reads.
//
// The size is an int in go-github and an int64 here, which is what the instance says;
// an asset larger than two gigabytes on a 32-bit build would be truncated, and nothing
// reads the size anyway.
func (a *asset) asset() *gogithub.ReleaseAsset {
	size := int(a.Size)
	state := assetState
	return &gogithub.ReleaseAsset{
		ID:                 &a.ID,
		Name:               &a.Name,
		Size:               &size,
		State:              &state,
		BrowserDownloadURL: &a.BrowserDownloadURL,
		CreatedAt:          &gogithub.Timestamp{Time: a.CreatedAt},
	}
}

// maxPerPage is the largest page Codeberg answers with. An instance can be configured
// to answer with fewer, which is why a short page is not read as the last one.
const maxPerPage = 50

// paging is the page and the page size to ask for, from what a GitHub caller asked for.
func paging(opts *gogithub.ListOptions) (int, int) {
	page, limit := 1, maxPerPage
	if opts != nil {
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.PerPage > 0 && opts.PerPage < maxPerPage {
			limit = opts.PerPage
		}
	}
	return page, limit
}

// response says whether there is another page, the way go-github's callers ask.
//
// A full page is followed by another, which is one request more than necessary when the
// list happens to end there. The alternative is reading a short page as the end, and an
// instance that answers with fewer items than were asked for would then hide the rest.
func response(page, limit, got int) *gogithub.Response {
	resp := &gogithub.Response{}
	if got > 0 && got >= limit {
		resp.NextPage = page + 1
	}
	return resp
}

// said is the start of what an instance answered with, on one line, for an error to carry.
func said(b []byte) string {
	const maxLen = 200
	r := []rune(strings.Join(strings.Fields(string(b)), " "))
	if len(r) > maxLen {
		return string(r[:maxLen]) + "..."
	}
	return string(r)
}

// For is the client reading the instance a package is on, and nil for a package that is
// on none.
//
// Which client is the type's: the Gitea family's API and GitLab's answer the same
// questions in different words, and a definition says which forge it is on rather than
// leaving it to be discovered.
func For(httpClient *http.Client, typ, host string) genrgst.RepositoriesService {
	switch typ {
	case aquaregistry.PkgInfoTypeForgejoRelease, aquaregistry.PkgInfoTypeGiteaRelease:
		return New(httpClient, host)
	case aquaregistry.PkgInfoTypeGitLabRelease:
		return NewGitLab(httpClient, host)
	}
	return nil
}
