package g2

import (
	"context"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v92/github"
)

// TemplateDir is the directory on main holding the files a new package branch starts
// with.
//
// A package branch needs the CI workflow before a pull request into it can be
// checked, because a pull_request workflow is read from the branch the pull request
// targets. The files live on main so that changing them changes what every new
// branch gets.
const TemplateDir = "templates/pkg"

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

// EnsurePackageBranch creates the package's branch if it doesn't exist and returns
// the commit it points at.
//
// The branch is an orphan: it shares no history with main, which is what keeps a
// commit to one package from rewriting a tree that every other package is under. Its
// first commit carries the template files rather than being empty, so that a pull
// request into it is checked by the same CI as everywhere else.
func (c *Client) EnsurePackageBranch(ctx context.Context, pkgName string) (string, error) {
	branch := BranchName(pkgName)
	sha, err := c.BranchSHA(ctx, branch)
	if err != nil {
		return "", err
	}
	if sha != "" {
		return sha, nil
	}

	entries, err := c.templateEntries(ctx)
	if err != nil {
		return "", err
	}
	tree, _, err := c.gh.Git.CreateTree(ctx, c.owner, c.repo, "", entries)
	if err != nil {
		return "", fmt.Errorf("create the tree of a package branch: %w", err)
	}
	commit, _, err := c.gh.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
		Message: new("chore: create the branch of " + pkgName),
		Tree:    tree,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create the first commit of a package branch: %w", err)
	}
	if _, _, err := c.gh.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: commit.GetSHA(),
	}); err != nil {
		return "", fmt.Errorf("create a package branch: %w", err)
	}
	return commit.GetSHA(), nil
}

// templateEntries returns the files a new package branch starts with, read from main.
func (c *Client) templateEntries(ctx context.Context) ([]*gogithub.TreeEntry, error) {
	_, contents, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, TemplateDir, nil)
	if err != nil {
		if refNotFound(resp) {
			// Without templates a package branch still works; its pull requests just
			// aren't checked by anything.
			return nil, nil
		}
		return nil, fmt.Errorf("list the template files: %w", err)
	}
	entries := make([]*gogithub.TreeEntry, 0, len(contents))
	for _, content := range contents {
		if content.GetType() != "file" {
			continue
		}
		entries = append(entries, &gogithub.TreeEntry{
			// The template's own directory is stripped: the files sit at the root of
			// the package branch, where a workflow has to be to run.
			Path: new(".github/workflows/" + content.GetName()),
			Mode: new("100644"),
			Type: new("blob"),
			SHA:  content.SHA,
		})
	}
	return entries, nil
}
