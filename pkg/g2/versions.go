package g2

import (
	"context"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v92/github"
)

// VersionDir is the directory holding the generated registry.json files on a
// package's branch.
const VersionDir = "versions"

// Client reads and writes aqua-registry-g2.
type Client struct {
	gh    *gogithub.Client
	owner string
	repo  string
	// branchGH creates the package branches. It is separate because creating one
	// has to get past the ruleset requiring status checks, which a brand new branch
	// can't have, while everything else must not.
	//
	// The token behind it is a GitHub App installation token whose app is listed as
	// a bypass actor for that ruleset and holds no pull-requests permission. It
	// therefore cannot open or merge a pull request, so the bypass can't be turned
	// into a way to land an unchecked change. When it isn't configured, branches are
	// created with the ordinary client, which works wherever no such ruleset exists.
	branchGH *gogithub.Client
	// prGH commits to the head branches and opens the pull requests. It is separate
	// because a pull request opened or updated with GITHUB_TOKEN gets its workflow
	// runs in an approval-required state, so nothing would check one until a person
	// pressed a button and auto-merge would never fire.
	//
	// The token behind it is a GitHub App installation token, which doesn't carry
	// that restriction. Its app is a bypass actor for nothing: it opens pull
	// requests and that is all, so the checks still decide what merges. When it
	// isn't configured the ordinary client is used, which is what a local run does.
	prGH *gogithub.Client
}

// New creates a Client. branchGH and prGH may be nil.
func New(gh, branchGH, prGH *gogithub.Client, owner, repo string) *Client {
	if branchGH == nil {
		branchGH = gh
	}
	if prGH == nil {
		prGH = gh
	}
	return &Client{gh: gh, branchGH: branchGH, prGH: prGH, owner: owner, repo: repo}
}

// Versions returns the versions of the package whose registry.json is in the
// repository.
//
// The repository is asked rather than a local record of what has been generated,
// because what matters is whether a version is merged, not whether it was once
// produced. A pull request that fails CI is never merged, and a record of "already
// generated" would stop it from ever being retried — exactly for the packages that
// need attention. Asking the repository makes a run idempotent instead.
func (c *Client) Versions(ctx context.Context, pkgName string) (map[string]struct{}, error) {
	_, contents, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, VersionDir,
		&gogithub.RepositoryContentGetOptions{Ref: BranchName(pkgName)})
	if err != nil {
		// A package with no generated version yet has no branch, and a brand new
		// repository has none at all. Either way there is nothing to skip.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("list the versions of the package: %w", err)
	}
	versions := make(map[string]struct{}, len(contents))
	for _, content := range contents {
		if content.GetType() != "dir" {
			continue
		}
		versions[content.GetName()] = struct{}{}
	}
	return versions, nil
}
