package g2

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// IndexFileName is the catalogue on the default branch.
const IndexFileName = aquag2.IndexFileName

// IndexBranch is where an update to the catalogue is committed.
//
// One fixed branch rather than one per run: the catalogue is only ever added to, so
// two runs writing to the same branch produce the same thing as two runs one after
// the other. A run whose pull request is still open adds to it instead of opening
// another, which is what keeps a scheduled reconciliation from leaving a trail of
// them behind.
const IndexBranch = HeadBranchPrefix + "index"

// Index returns the catalogue, or an empty one when the repository has none yet.
func (c *Client) Index(ctx context.Context, ref string) (*aquag2.Index, error) {
	body, err := c.File(ctx, ref, IndexFileName)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return &aquag2.Index{}, nil
	}
	index, err := aquag2.ReadIndex(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("read the catalogue: %w", err)
	}
	return index, nil
}

// File returns a file of the repository, or an empty string when there is none.
//
// Absent isn't a failure. A file the registry hasn't been written with yet -- one added
// to what a run maintains after the registry was created -- is missing rather than
// wrong, and what reads it is deciding whether to write it.
func (c *Client) File(ctx context.Context, ref, path string) (string, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, path,
		&gogithub.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", fmt.Errorf("get %s: %w", path, err)
	}
	body, err := content.GetContent()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return body, nil
}

// IndexPullRequest returns the open pull request updating the catalogue, or nil.
func (c *Client) IndexPullRequest(ctx context.Context) (*gogithub.PullRequest, error) {
	opts := &gogithub.PullRequestListOptions{
		State: "open",
		Head:  c.owner + ":" + IndexBranch,
	}
	opts.PerPage = 1
	prs, resp, err := c.gh.PullRequests.List(ctx, c.owner, c.repo, opts)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("list open pull requests: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil //nolint:nilnil
	}
	return prs[0], nil
}

// CreateIndexPullRequest opens a pull request updating the catalogue.
func (c *Client) CreateIndexPullRequest(ctx context.Context, base, title, body string) (*gogithub.PullRequest, error) {
	return c.CreatePullRequestFrom(ctx, IndexBranch, base, title, body)
}
