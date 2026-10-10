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
	// Deleted takes the path out of the tree instead of writing it. The entry is sent
	// with a null sha, which is how the Git data API says a path is gone.
	Deleted bool
}

const (
	blobMode = "100644"
	blobType = "blob"
)

// deletedEntry is the tree entry that takes a path out.
//
// An entry with no content and no sha is serialized with "sha": null, which is how the Git
// data API says the path is gone. The mode and the type go with it: the API answers "Must
// supply a valid tree.mode" to an entry without a mode even when it is only removing one,
// where go-github documents both as ignored.
func deletedEntry(path string) *gogithub.TreeEntry {
	return &gogithub.TreeEntry{
		Path: new(path),
		Mode: new(blobMode),
		Type: new(blobType),
	}
}

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
	// Force, because a head branch left behind by an earlier run was written against a
	// base that has since moved, so what it holds is stale.
	return c.commit(ctx, c.prGH, true, branch, parent, message, files)
}

// Push writes the files straight onto a package branch.
//
// A package branch takes changes through a pull request, which is what the registry's
// review is, and the writer here is the one app that bypasses that requirement. What it
// is for is a file the review has nothing to say about: one derived from what the branch
// already holds, where a pull request per package would be hundreds of pull requests
// asserting what their own diff already proves.
//
// It never forces. A package branch is the registry, and a push that isn't a
// fast-forward would be rewriting what has been published rather than adding to it.
func (c *Client) Push(ctx context.Context, branch, parent, message string, files []*File) error {
	return c.commit(ctx, c.branchGH, false, branch, parent, message, files)
}

// commit builds the commit and moves the branch to it.
func (c *Client) commit(ctx context.Context, gh *gogithub.Client, force bool, branch, parent, message string, files []*File) error {
	entries := make([]*gogithub.TreeEntry, 0, len(files))
	for _, file := range files {
		if file.Deleted {
			entries = append(entries, deletedEntry(file.Path))
			continue
		}
		entries = append(entries, &gogithub.TreeEntry{
			Path:    new(file.Path),
			Mode:    new(blobMode),
			Type:    new(blobType),
			Content: new(file.Content),
		})
	}
	return c.commitEntries(ctx, gh, force, branch, parent, message, entries)
}

// commitEntries builds the commit from tree entries, which is what a file carrying content
// and a file naming a blob the repository already holds both come to.
func (c *Client) commitEntries(ctx context.Context, gh *gogithub.Client, force bool, branch, parent, message string, entries []*gogithub.TreeEntry) error {
	parentCommit, _, err := c.gh.Git.GetCommit(ctx, c.owner, c.repo, parent)
	if err != nil {
		return fmt.Errorf("get the parent commit: %w", err)
	}

	// The parent's tree is the base, so the commit adds files rather than replacing
	// everything the branch holds.
	tree, _, err := gh.Git.CreateTree(ctx, c.owner, c.repo, parentCommit.GetTree().GetSHA(), entries)
	if err != nil {
		return fmt.Errorf("create a tree: %w", err)
	}

	commit, _, err := gh.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
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
		_, _, err = gh.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
			Ref: ref,
			SHA: commit.GetSHA(),
		})
		if err != nil {
			return fmt.Errorf("create the branch: %w", err)
		}
		return nil
	}
	_, _, err = gh.Git.UpdateRef(ctx, c.owner, c.repo, "heads/"+branch, gogithub.UpdateRef{
		SHA:   commit.GetSHA(),
		Force: new(force),
	})
	if err != nil {
		return fmt.Errorf("update the branch: %w", err)
	}
	return nil
}
