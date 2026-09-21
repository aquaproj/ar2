package g2

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v92/github"
)

// pullRequestsPerPage is the page size used to list open pull requests.
const pullRequestsPerPage = 100

// PackagesInFlight returns the packages that already have an open pull request.
//
// Asking the repository which versions it holds can't see a pull request that hasn't
// merged yet, so without this a run would open a second pull request for work that
// is already waiting — including work waiting precisely because its CI failed.
//
// Every open pull request is listed once per run rather than checking each package,
// because a run touches many packages and there are rarely many pull requests.
func (c *Client) PackagesInFlight(ctx context.Context) (map[string]struct{}, error) {
	inFlight := map[string]struct{}{}
	opts := &gogithub.PullRequestListOptions{State: "open"}
	opts.PerPage = pullRequestsPerPage
	for {
		prs, resp, err := c.gh.PullRequests.List(ctx, c.owner, c.repo, opts)
		if err != nil {
			// The repository doesn't exist yet while aqua-registry-g2 is being set
			// up. Nothing is in flight then.
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return inFlight, nil
			}
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		for _, pr := range prs {
			branch := pr.GetHead().GetRef()
			if !strings.HasPrefix(branch, HeadBranchPrefix) {
				continue
			}
			inFlight[branch] = struct{}{}
		}
		if resp.NextPage == 0 {
			return inFlight, nil
		}
		opts.Page = resp.NextPage
	}
}

// CreatePullRequest opens a pull request from the package's head branch into its
// package branch.
func (c *Client) CreatePullRequest(ctx context.Context, pkgName, title, body string) (*gogithub.PullRequest, error) {
	pr, _, err := c.gh.PullRequests.Create(ctx, c.owner, c.repo, gogithub.CreatePullRequest{
		Title: new(title),
		Body:  new(body),
		Head:  HeadBranchName(pkgName),
		Base:  BranchName(pkgName),
	})
	if err != nil {
		return nil, fmt.Errorf("create a pull request: %w", err)
	}
	return pr, nil
}
