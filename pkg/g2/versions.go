package g2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"

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
	// version is the ar2 that is running. Every pull request it opens is labelled with
	// it, so that the ones an older ar2 made can be found together.
	version string
	// ids is what each package's branch is named after. Every branch addressed here is
	// resolved through it: a branch's name is an id, and the definition on the branch is
	// the only thing that says which package it holds.
	ids *Identities
}

// UseIdentities tells the client what each package's branch is named after.
//
// Read once, before anything is read or written, because every branch this addresses is
// named after an id. A client that was never told holds no package as far as the readers
// are concerned, and refuses to create a branch rather than inventing where to put one.
func (c *Client) UseIdentities(ids *Identities) {
	c.ids = ids
}

// Branch is the branch holding the package, and false when the registry holds none.
func (c *Client) Branch(pkgName string) (string, bool) {
	return c.ids.Branch(pkgName)
}

// HeadBranch is the branch a pull request for the package is opened from, and false when
// the registry holds no branch for it.
func (c *Client) HeadBranch(pkgName string) (string, bool) {
	return c.ids.HeadBranch(pkgName)
}

// VersionHeadBranch is the branch a pull request for one version alone is opened from.
func (c *Client) VersionHeadBranch(pkgName, version string) (string, bool) {
	return c.ids.VersionHeadBranch(pkgName, version)
}

// RemoveBranch is the branch the pull request that stops serving the package is opened from.
func (c *Client) RemoveBranch(pkgName string) (string, bool) {
	return c.ids.RemoveBranch(pkgName)
}

// IsVersionHeadBranch reports whether the branch is one of the package's version branches.
func (c *Client) IsVersionHeadBranch(pkgName, branch string) bool {
	return c.ids.IsVersionHeadBranch(pkgName, branch)
}

// New creates a Client. branchGH and prGH may be nil, and an empty version labels nothing.
func New(gh, branchGH, prGH *gogithub.Client, owner, repo, version string) *Client {
	if branchGH == nil {
		branchGH = gh
	}
	if prGH == nil {
		prGH = gh
	}
	return &Client{gh: gh, branchGH: branchGH, prGH: prGH, owner: owner, repo: repo, version: version}
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
// The Git Data API rather than the Contents API, which stops at 1,000 entries in a
// directory and says so only by returning fewer: a package with more versions than that
// would have looked as though the ones it didn't return were missing, and been generated
// again on every run.
func (c *Client) Versions(ctx context.Context, logger *slog.Logger, pkgName string) (map[string]struct{}, error) {
	branch, ok := c.Branch(pkgName)
	if !ok {
		// A package the registry doesn't hold yet. There is nothing to skip.
		return map[string]struct{}{}, nil
	}
	return c.VersionsOnRef(ctx, logger, branch)
}

// VersionsOnRef returns the versions a ref holds a registry.json for.
//
// Which for a package branch is what the registry holds, and for the head branch of a pull
// request is what that pull request would add. The second is what a regeneration of something
// not yet merged works from: the versions are on the branch and nowhere else.
func (c *Client) VersionsOnRef(ctx context.Context, logger *slog.Logger, ref string) (map[string]struct{}, error) {
	// The Git Data API rather than the Contents API, which stops at 1,000 entries in a
	// directory and says so only by returning fewer.
	//
	// Read recursively, because a version is the directory holding the generated file
	// rather than any directory under versions/. See versionOf.
	tree, resp, err := c.gh.Git.GetTree(ctx, c.owner, c.repo, ref+":"+aquag2.VersionDir, true)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			// The branch holds no versions directory, which is a branch carrying nothing
			// but a definition.
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("get the versions directory of %s: %w", ref, err)
	}
	if tree.GetTruncated() {
		// The limit is 100,000 entries, so reaching it means something other than a
		// package's version history. A partial list would quietly leave versions out.
		return nil, fmt.Errorf("%w: %s", errTreeTruncated, ref)
	}
	logger.Debug("listed the versions of a ref", "ref", ref, "num_of_entries", len(tree.Entries))
	out := make(map[string]struct{}, len(tree.Entries))
	for _, entry := range tree.Entries {
		version, ok := versionOf(entry)
		if !ok {
			continue
		}
		out[version] = struct{}{}
	}
	return out, nil
}

// versionOf reads the version whose registry.json a tree entry is, and reports false for
// an entry that is not one.
//
// A version's name is escaped into one path segment, so it is the directory holding the
// generated file. Before it was escaped, a version with a slash in it became two
// directories, and calling every directory a version made "kustomize" a version of
// kustomize: what was reported as already generated was a version that doesn't exist,
// while the one that does looked missing.
func versionOf(entry *gogithub.TreeEntry) (string, bool) {
	if entry.GetType() != blobType {
		return "", false
	}
	dir, file := path.Split(entry.GetPath())
	if file != aquag2.FileName {
		return "", false
	}
	return aquag2.DecodeVersion(strings.TrimSuffix(dir, "/"))
}

// errTreeTruncated is what a listing GitHub wouldn't give whole gets.
var errTreeTruncated = errors.New("the versions directory has more entries than GitHub returns")
