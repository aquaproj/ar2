package g2

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// File is a file to commit.
type File struct {
	// Path is where the file goes: from the root of the repository for Commit, and from
	// the package's directory for CommitPackage, such as versions/v1.2.3/registry-1.json.
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

// Commit writes the files onto a new commit on top of parent and points branch at it. The
// paths are from the root of the repository.
//
// The commit is built through the Git data API rather than a checkout. The default branch
// holds every package, so cloning it to write one file would be the expensive way to do
// this.
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
//
// It forces, because a head branch left behind by an earlier run was written against a
// base that has since moved, so what it holds is stale.
func (c *Client) Commit(ctx context.Context, branch, parent, message string, files []*File) error {
	sha, err := c.commitTree(ctx, parent, message, treeEntries("", files))
	if err != nil {
		return err
	}
	return c.moveBranch(ctx, branch, sha)
}

// CommitPackage writes the package's files onto a new commit on top of parent and points
// branch at it. The paths are from the package's directory, such as
// versions/v1.2.3/registry-1.json.
//
// A commit that changes the versions also writes versions.json, the list of them, so that
// the list arrives in the same pull request as what it lists and is never behind the
// registry. Two commits rather than one: the list records the sha of the versions
// directory it was made from, which is known only once there is a tree holding them. The
// pull request is squashed when it merges, so the default branch gets one.
//
// Everything Commit says about how and by whom applies here too.
func (c *Client) CommitPackage(ctx context.Context, logger *slog.Logger, pkgName, branch, parent, message string, files []*File) error {
	dir, ok := c.Dir(pkgName)
	if !ok {
		return fmt.Errorf("%w: %s", errNoIdentity, pkgName)
	}
	sha, err := c.commitTree(ctx, parent, message, treeEntries(dir+"/", files))
	if err != nil {
		return err
	}
	if touchesVersions(files) {
		listed, err := c.commitVersionsList(ctx, logger, dir, parent, sha, files)
		if err != nil {
			return err
		}
		sha = listed
	}
	return c.moveBranch(ctx, branch, sha)
}

// CommitWaiting writes the package's files onto a new commit on top of parent and points
// branch at it, the way CommitPackage does, but leaves versions.json alone.
//
// It is for the pull request of versions waiting for a definition, which a person merges
// whenever the definition is written. The package's own pull requests go on merging in the
// meantime and each writes the list, so a list written here too would conflict with
// whichever of them merged first -- every time, since that is what happens while a person
// is away. Left alone, the list is behind once this merges, and the next commit for the
// package sees that its source isn't the versions directory any more and makes it again.
func (c *Client) CommitWaiting(ctx context.Context, pkgName, branch, parent, message string, files []*File) error {
	dir, ok := c.Dir(pkgName)
	if !ok {
		return fmt.Errorf("%w: %s", errNoIdentity, pkgName)
	}
	sha, err := c.commitTree(ctx, parent, message, treeEntries(dir+"/", files))
	if err != nil {
		return err
	}
	return c.moveBranch(ctx, branch, sha)
}

// touchesVersions reports whether any of the files is under versions/.
func touchesVersions(files []*File) bool {
	for _, file := range files {
		if strings.HasPrefix(file.Path, aquag2.VersionDir+"/") {
			return true
		}
	}
	return false
}

// treeEntries is the files as tree entries, each path under prefix.
func treeEntries(prefix string, files []*File) []*gogithub.TreeEntry {
	entries := make([]*gogithub.TreeEntry, 0, len(files))
	for _, file := range files {
		if file.Deleted {
			entries = append(entries, deletedEntry(prefix+file.Path))
			continue
		}
		entries = append(entries, &gogithub.TreeEntry{
			Path:    new(prefix + file.Path),
			Mode:    new(blobMode),
			Type:    new(blobType),
			Content: new(file.Content),
		})
	}
	return entries
}

// commitTree creates a commit on top of parent whose tree is parent's with the entries
// written over it, and returns the commit. No branch moves.
func (c *Client) commitTree(ctx context.Context, parent, message string, entries []*gogithub.TreeEntry) (string, error) {
	parentCommit, _, err := c.gh.Git.GetCommit(ctx, c.owner, c.repo, parent)
	if err != nil {
		return "", fmt.Errorf("get the parent commit: %w", err)
	}

	// The parent's tree is the base, so the commit adds files rather than replacing
	// everything the default branch holds.
	tree, err := c.buildTree(ctx, parentCommit.GetTree().GetSHA(), entries)
	if err != nil {
		return "", err
	}

	commit, _, err := c.prGH.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
		Message: new(message),
		Tree:    tree,
		Parents: []*gogithub.Commit{{SHA: new(parent)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create a commit: %w", err)
	}
	return commit.GetSHA(), nil
}

// entriesPerTree is how many entries one request writes into a tree.
//
// GitHub gives up on a request that takes too long to process, and a tree of a thousand
// entries is one: moving the packages off their branches, 1,135 entries in one request, was
// answered first with a 502 and then with "your request timed out ... Consider building the
// tree incrementally". A hundred is far below that, and an ordinary pull request -- one
// package's new versions -- fits in a single request as before.
const entriesPerTree = 100

// buildTree writes the entries over the base tree, a hundred at a time, each request on top
// of the tree the one before it made, and returns the last.
func (c *Client) buildTree(ctx context.Context, base string, entries []*gogithub.TreeEntry) (*gogithub.Tree, error) {
	var tree *gogithub.Tree
	for start := 0; start < len(entries) || tree == nil; start += entriesPerTree {
		batch := entries[start:min(start+entriesPerTree, len(entries))]
		created, _, err := c.prGH.Git.CreateTree(ctx, c.owner, c.repo, base, batch)
		if err != nil {
			return nil, fmt.Errorf("create a tree: %w", err)
		}
		tree = created
		base = created.GetSHA()
	}
	return tree, nil
}

// moveBranch points the branch at the commit, creating it when it doesn't exist.
func (c *Client) moveBranch(ctx context.Context, branch, commit string) error {
	sha, err := c.BranchSHA(ctx, branch)
	if err != nil {
		return err
	}
	if sha == "" {
		_, _, err = c.prGH.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
			Ref: "refs/heads/" + branch,
			SHA: commit,
		})
		if err != nil {
			return fmt.Errorf("create the branch: %w", err)
		}
		return nil
	}
	_, _, err = c.prGH.Git.UpdateRef(ctx, c.owner, c.repo, "heads/"+branch, gogithub.UpdateRef{
		SHA:   commit,
		Force: new(true),
	})
	if err != nil {
		return fmt.Errorf("update the branch: %w", err)
	}
	return nil
}
