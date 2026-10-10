package g2

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v92/github"
)

// errNoDefaultBranch says the repository has no default branch to write on top of.
var errNoDefaultBranch = errors.New("the repository has no default branch")

// refNotFound reports whether an error is GitHub saying the ref doesn't exist.
func refNotFound(resp *gogithub.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusNotFound
}

// BranchSHA returns the commit a branch points at, or an empty string when the
// branch doesn't exist.
func (c *Client) BranchSHA(ctx context.Context, branch string) (string, error) {
	ref, resp, err := c.gh.Git.GetRef(ctx, c.owner, c.repo, "heads/"+branch)
	if err != nil {
		if refNotFound(resp) {
			return "", nil
		}
		return "", fmt.Errorf("get the branch: %w", err)
	}
	return ref.GetObject().GetSHA(), nil
}

// PackageBase gives the package an id when the registry doesn't hold it yet, and returns
// the commit a pull request for it is written on top of: the default branch's.
//
// The id is minted here and nowhere else, which is the moment the package is taken over.
// Nothing is written for it until the pull request is: the head branch named after the id
// is what the next reading of the registry finds it by until that merges.
func (c *Client) PackageBase(ctx context.Context, pkgName string) (string, error) {
	if c.ids == nil {
		return "", errNoIdentities
	}
	if _, held := c.ids.ID(pkgName); !held {
		c.ids.Mint(pkgName)
	}
	sha, err := c.BranchSHA(ctx, DefaultBranch)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", fmt.Errorf("%w: %s", errNoDefaultBranch, DefaultBranch)
	}
	return sha, nil
}
