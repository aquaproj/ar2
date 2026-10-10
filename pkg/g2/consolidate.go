package g2

import (
	"context"
	"fmt"
	"slices"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// legacyBranchPrefix marks the branches each package was kept on before every package moved
// onto the default branch.
const legacyBranchPrefix = "pkg_"

// ConsolidateBranch is the head branch of the pull request that moves the packages.
const ConsolidateBranch = HeadBranchPrefix + "consolidate"

// branchesPerPage is how many branches one request lists.
const branchesPerPage = 100

// LegacyPackage is a package still on a branch of its own, and what that branch holds that
// moves with it.
type LegacyPackage struct {
	ID     string
	Branch string
	// Entries are the branch's definition, its list of versions and its versions
	// directory, as the root of its tree names them. What else the branch holds -- its
	// README, its workflows -- stays behind with it.
	Entries []*gogithub.TreeEntry
}

// LegacyPackages is every package still on a branch of its own, in id order.
//
// A request per hundred branches to list them, and one per branch for the root of its tree:
// the root is three entries, and the versions directory is copied by its sha rather than
// listed.
func (c *Client) LegacyPackages(ctx context.Context) ([]*LegacyPackage, error) {
	opts := &gogithub.BranchListOptions{}
	opts.PerPage = branchesPerPage
	out := []*LegacyPackage{}
	for {
		page, resp, err := c.gh.Repositories.ListBranches(ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list the branches: %w", err)
		}
		for _, branch := range page {
			id, ok := strings.CutPrefix(branch.GetName(), legacyBranchPrefix)
			if !ok || !IsID(id) {
				continue
			}
			pkg, err := c.legacyPackage(ctx, id, branch)
			if err != nil {
				return nil, err
			}
			out = append(out, pkg)
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	slices.SortFunc(out, func(a, b *LegacyPackage) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// moves reports whether an entry at the root of a package branch moves with the package.
func moves(name string) bool {
	switch name {
	case ConfigFileName, aquag2.VersionsFileName, aquag2.VersionDir:
		return true
	}
	return false
}

func (c *Client) legacyPackage(ctx context.Context, id string, branch *gogithub.Branch) (*LegacyPackage, error) {
	tree, _, err := c.gh.Git.GetTree(ctx, c.owner, c.repo, branch.GetCommit().GetSHA(), false)
	if err != nil {
		return nil, fmt.Errorf("get the tree of %s: %w", branch.GetName(), err)
	}
	pkg := &LegacyPackage{ID: id, Branch: branch.GetName()}
	for _, entry := range tree.Entries {
		if moves(entry.GetPath()) {
			pkg.Entries = append(pkg.Entries, entry)
		}
	}
	return pkg, nil
}

// ConsolidatedEntries are the tree entries that put each package in its directory on the
// default branch.
//
// Each names the object the branch already holds -- the blob of a file, the tree of the
// versions directory -- so nothing is uploaded and nothing is rewritten: what each package
// serves is the same bytes it served from its branch, and versions.json's source, the sha of
// the versions directory, is still the directory it describes.
func ConsolidatedEntries(pkgs []*LegacyPackage) []*gogithub.TreeEntry {
	out := []*gogithub.TreeEntry{}
	for _, pkg := range pkgs {
		dir := PackageDir(pkg.ID)
		for _, entry := range pkg.Entries {
			out = append(out, &gogithub.TreeEntry{
				Path: new(dir + "/" + entry.GetPath()),
				Mode: entry.Mode,
				Type: entry.Type,
				SHA:  entry.SHA,
			})
		}
	}
	return out
}

// CommitEntries writes tree entries onto a new commit on top of parent and points branch at
// it. The entries name objects the repository already holds, or carry content.
func (c *Client) CommitEntries(ctx context.Context, branch, parent, message string, entries []*gogithub.TreeEntry) error {
	sha, err := c.commitTree(ctx, parent, message, entries)
	if err != nil {
		return err
	}
	return c.moveBranch(ctx, branch, sha)
}

// CommitTree writes tree entries onto a new commit on top of parent and returns it, without
// pointing any branch at it.
func (c *Client) CommitTree(ctx context.Context, parent, message string, entries []*gogithub.TreeEntry) (string, error) {
	return c.commitTree(ctx, parent, message, entries)
}

// MoveBranch points the branch at the commit, creating it when it doesn't exist.
func (c *Client) MoveBranch(ctx context.Context, branch, commit string) error {
	return c.moveBranch(ctx, branch, commit)
}
