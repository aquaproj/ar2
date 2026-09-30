package g2

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// pullRequestsPerPage is the page size used to list open pull requests.
const pullRequestsPerPage = 100

// stateOpen asks GitHub for the pull requests that haven't been merged or closed.
const stateOpen = "open"

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
	opts := &gogithub.PullRequestListOptions{State: stateOpen}
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
func (c *Client) CreatePullRequest(ctx context.Context, logger *slog.Logger, pkgName, title, body string) (*gogithub.PullRequest, error) {
	head, ok := c.HeadBranch(pkgName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errNoIdentity, pkgName)
	}
	base, _ := c.Branch(pkgName)
	return c.CreatePullRequestFrom(ctx, logger, head, base, title, body)
}

// CreatePullRequestFrom opens a pull request from head into base.
//
// It is the pull request client that opens it rather than the reading one, whatever the
// branches are: a pull request opened with GITHUB_TOKEN gets its checks in an
// approval-required state, so nothing would check it until a person pressed a button.
func (c *Client) CreatePullRequestFrom(ctx context.Context, logger *slog.Logger, head, base, title, body string) (*gogithub.PullRequest, error) {
	pr, _, err := c.prGH.PullRequests.Create(ctx, c.owner, c.repo, gogithub.CreatePullRequest{
		Title: new(title),
		Body:  new(body),
		Head:  head,
		Base:  base,
	})
	if err != nil {
		return nil, fmt.Errorf("create a pull request: %w", err)
	}
	c.label(ctx, logger, pr.GetNumber())
	return pr, nil
}

// label marks the pull request with the ar2 that opened it.
//
// A pull request is read long after the run that made it, and what it is worth is partly
// which ar2 made it: an inference that has since been corrected made every pull request
// before the correction wrong in the same way, and there is no other way to tell those apart
// from the ones made after. Labelled, they can be found and closed as a group instead of one
// at a time -- which is what happened to the provenance, the asset choice and the minisign
// check, each found by reading one pull request and each affecting every package reached
// since.
//
// Said rather than failed for: the pull request is the work, and a label it didn't get is a
// pull request somebody still has to read.
func (c *Client) label(ctx context.Context, logger *slog.Logger, number int) {
	if c.version == "" {
		// A local build says nothing about which release it is, and a label saying so
		// would be worse than none.
		return
	}
	name := labelName(c.version)
	if err := c.ensureLabel(ctx, name); err != nil {
		slogerr.WithError(logger, err).Warn("create the label of this ar2", "label", name)
		return
	}
	if _, _, err := c.prGH.Issues.AddLabelsToIssue(ctx, c.owner, c.repo, number, []string{name}); err != nil {
		slogerr.WithError(logger, err).Warn("label the pull request", "number", number, "label", name)
	}
}

// NeedsDefinitionLabel marks the pull requests whose versions are waiting for a definition.
//
// What it is for is finding them together: they are the one kind of pull request that doesn't
// merge itself, and a maintainer who has half an hour for the registry wants the list.
const NeedsDefinitionLabel = "needs-definition"

// Label puts a label on a pull request, making it first when the repository hasn't got it.
//
// Said rather than failed for, like the label of the release: what the pull request carries
// matters more than what it is filed under.
func (c *Client) Label(ctx context.Context, logger *slog.Logger, number int, name string) {
	if err := c.ensureLabel(ctx, name); err != nil {
		slogerr.WithError(logger, err).Warn("create a label", "label", name)
		return
	}
	if _, _, err := c.prGH.Issues.AddLabelsToIssue(ctx, c.owner, c.repo, number, []string{name}); err != nil {
		slogerr.WithError(logger, err).Warn("label the pull request", "number", number, "label", name)
	}
}

// labelName is what the label is called: ar2:v0.0.30, whichever way the version was written.
func labelName(version string) string {
	if strings.HasPrefix(version, "v") {
		return "ar2:" + version
	}
	return "ar2:v" + version
}

// ensureLabel creates the label when the repository doesn't have it.
//
// A label is per repository, so the first pull request a release opens is the one that makes
// it. Adding one that doesn't exist is refused rather than created, so this can't be left to
// the call that uses it.
func (c *Client) ensureLabel(ctx context.Context, name string) error {
	if _, _, err := c.prGH.Issues.GetLabel(ctx, c.owner, c.repo, name); err == nil {
		return nil
	}
	_, resp, err := c.prGH.Issues.CreateLabel(ctx, c.owner, c.repo, gogithub.CreateIssueLabelRequest{
		Name:  name,
		Color: new(labelColor),
	})
	if err == nil {
		return nil
	}
	if resp != nil && resp.StatusCode == http.StatusUnprocessableEntity {
		// Already there, which is what two runs opening their first pull request at the
		// same time look like.
		return nil
	}
	return fmt.Errorf("create a label: %w", err)
}

// labelColor is GitHub's own default, because the label says what made the pull request
// rather than how much it matters.
const labelColor = "ededed"

// WaitingPullRequest returns the package's open pull request of versions waiting for a
// definition, or nil when it has none.
//
// It is the one whose head branch is named after a version rather than after the package: the
// pull request a run opens for everything it generated is on the package's own branch, and
// this is the other kind.
func (c *Client) WaitingPullRequest(ctx context.Context, pkgName string) (*gogithub.PullRequest, error) {
	opts := &gogithub.PullRequestListOptions{State: stateOpen}
	opts.PerPage = pullRequestsPerPage
	for {
		prs, resp, err := c.gh.PullRequests.List(ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		for _, pr := range prs {
			if IsVersionHeadBranch(pkgName, pr.GetHead().GetRef()) {
				return pr, nil
			}
		}
		if resp.NextPage == 0 {
			return nil, nil //nolint:nilnil // no pull request is waiting, which isn't a failure
		}
		opts.Page = resp.NextPage
	}
}
