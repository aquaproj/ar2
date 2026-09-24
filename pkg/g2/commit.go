package g2

import (
	"context"
	"fmt"

	gogithub "github.com/google/go-github/v92/github"
)

// File is a file to commit.
type File struct {
	// Path is where the file goes on the package branch, such as
	// versions/v1.2.3/registry-1.json.
	Path    string
	Content string
}

const (
	blobMode = "100644"
	blobType = "blob"
)

// Commit writes the files onto a new commit on top of parent and points branch at it.
//
// The commit is built through the Git data API rather than a checkout. A package
// branch is an orphan and there are as many of them as there are packages, so
// cloning to write one file would be the expensive way to do this.
//
// GitHub signs the commits it creates this way when the caller is a GitHub App
// installation or GitHub Actions, which is how ar2 runs, so they satisfy a ruleset
// requiring signatures. A user access token produces unsigned commits, so a local
// run can't produce something a signature ruleset accepts; that only affects trying
// ar2 out by hand.
//
// It is the pull request's client that writes, not the reading one. A commit onto a
// branch that already has an open pull request is a synchronize event, and one made
// with GITHUB_TOKEN leaves that pull request's checks waiting for approval.
func (c *Client) Commit(ctx context.Context, branch, parent, message string, files []*File) error {
	parentCommit, _, err := c.gh.Git.GetCommit(ctx, c.owner, c.repo, parent)
	if err != nil {
		return fmt.Errorf("get the parent commit: %w", err)
	}

	entries := make([]*gogithub.TreeEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, &gogithub.TreeEntry{
			Path:    new(file.Path),
			Mode:    new(blobMode),
			Type:    new(blobType),
			Content: new(file.Content),
		})
	}
	// The parent's tree is the base, so the commit adds files rather than replacing
	// everything the branch holds.
	tree, _, err := c.prGH.Git.CreateTree(ctx, c.owner, c.repo, parentCommit.GetTree().GetSHA(), entries)
	if err != nil {
		return fmt.Errorf("create a tree: %w", err)
	}

	commit, _, err := c.prGH.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
		Message: new(message),
		Tree:    tree,
		Parents: []*gogithub.Commit{{SHA: new(parent)}},
	}, nil)
	if err != nil {
		return fmt.Errorf("create a commit: %w", err)
	}

	ref := "refs/heads/" + branch
	sha, err := c.BranchSHA(ctx, branch)
	if err != nil {
		return err
	}
	if sha == "" {
		_, _, err = c.prGH.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
			Ref: ref,
			SHA: commit.GetSHA(),
		})
		if err != nil {
			return fmt.Errorf("create the branch: %w", err)
		}
		return nil
	}
	// A branch left behind by an earlier run is reset rather than added to: it was
	// written against a base that has since moved, so what it holds is stale.
	_, _, err = c.prGH.Git.UpdateRef(ctx, c.owner, c.repo, "heads/"+branch, gogithub.UpdateRef{
		SHA:   commit.GetSHA(),
		Force: new(true),
	})
	if err != nil {
		return fmt.Errorf("update the branch: %w", err)
	}
	return nil
}
