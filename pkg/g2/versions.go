package g2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

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
//
// The listing is aqua's own, which reads the branch through the Git Data API. The
// Contents API stops at 1,000 entries in a directory and says so only by returning
// fewer, so a package with more versions than that would have looked as though the
// ones it didn't return were missing, and been generated again on every run.
func (c *Client) Versions(ctx context.Context, logger *slog.Logger, pkgName string) (map[string]struct{}, error) {
	versions, err := aquag2.NewVersionLister(c.gh.Git, c.owner, c.repo).List(ctx, logger, pkgName)
	if err != nil {
		// A package with no generated version yet has no branch, and a brand new
		// repository has none at all. Either way there is nothing to skip.
		if errors.Is(err, aquag2.ErrNoPackageBranch) {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("list the versions of the package: %w", err)
	}
	out := make(map[string]struct{}, len(versions))
	for _, v := range versions {
		out[v] = struct{}{}
	}
	return out, nil
}
