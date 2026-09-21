package g2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v92/github"
)

// TemplateDir is the directory on main holding the files a new package branch starts
// with. Its contents are copied to the branch at the same paths, so a workflow lives
// at templates/.github/workflows/ and lands at .github/workflows/.
//
// A package branch needs the CI workflow before a pull request into it can be
// checked, because a pull_request workflow is read from the branch the pull request
// targets. The files live on main so that changing them changes what every new
// branch gets.
const TemplateDir = "templates"

// errNoTemplate stops a package branch from being created without the workflow that
// checks pull requests into it. A branch ruleset requiring status checks is what
// actually keeps an unchecked change from merging; failing here means the branch is
// never created in a state where its pull requests can't pass, rather than leaving
// one behind that nothing can merge into.
var errNoTemplate = errors.New("main has no " + TemplateDir + " to start a package branch from")

const readmePath = "README.md"

// readmeContent explains what a package branch is to whoever opens it.
func readmeContent(pkgName string) string {
	return fmt.Sprintf(`# %s

This branch holds the generated registry.json of the aqua package %q, one per
version under `+"`versions/`"+`. It is created and updated by ar2 and shares no history
with the default branch.
`, pkgName, pkgName)
}

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
	// A tree has to hold something: GitHub rejects an empty one. The README also
	// tells anyone who lands on the branch what it is, which matters when a
	// repository has one branch per package and none of them look like a project.
	entries = append(entries, &gogithub.TreeEntry{
		Path:    new(readmePath),
		Mode:    new(blobMode),
		Type:    new(blobType),
		Content: new(readmeContent(pkgName)),
	})
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
//
// The whole subtree is fetched in one request rather than walking directories, and
// each path is kept as it is under TemplateDir. Mapping the paths here instead would
// put the layout of a package branch somewhere only ar2 knows about.
func (c *Client) templateEntries(ctx context.Context) ([]*gogithub.TreeEntry, error) {
	tree, resp, err := c.gh.Git.GetTree(ctx, c.owner, c.repo, "HEAD", true)
	if err != nil {
		if refNotFound(resp) {
			return nil, errNoTemplate
		}
		return nil, fmt.Errorf("get the tree of the default branch: %w", err)
	}
	prefix := TemplateDir + "/"
	entries := make([]*gogithub.TreeEntry, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		if entry.GetType() != blobType || !strings.HasPrefix(entry.GetPath(), prefix) {
			continue
		}
		entries = append(entries, &gogithub.TreeEntry{
			Path: new(strings.TrimPrefix(entry.GetPath(), prefix)),
			Mode: entry.Mode,
			Type: entry.Type,
			SHA:  entry.SHA,
		})
	}
	if len(entries) == 0 {
		return nil, errNoTemplate
	}
	return entries, nil
}
