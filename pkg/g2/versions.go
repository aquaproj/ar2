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

// Client reads aqua-registry-g2.
type Client struct {
	gh    *gogithub.Client
	owner string
	repo  string
}

// New creates a Client.
func New(gh *gogithub.Client, owner, repo string) *Client {
	return &Client{gh: gh, owner: owner, repo: repo}
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
