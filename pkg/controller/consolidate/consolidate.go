// Package consolidate moves every package from a branch of its own onto the default branch.
//
// It is done once. Each package was kept on an orphan branch, pkg_<id>, and is kept under
// pkgs/<shard>/<id>/ on the default branch from now on (aquaproj/aqua-registry-g2#695). The
// move is one pull request that names the objects the branches already hold, so it uploads
// nothing and changes no byte of what any package serves.
package consolidate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// Registry is what the move reads and writes.
type Registry interface {
	LegacyPackages(ctx context.Context) ([]*g2.LegacyPackage, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	CommitEntries(ctx context.Context, branch, parent, message string, entries []*gogithub.TreeEntry) error
	CreatePullRequestFrom(ctx context.Context, logger *slog.Logger, head, base, title, body string) (*gogithub.PullRequest, error)
}

// Controller moves the packages.
type Controller struct {
	g2 Registry
}

// New creates a Controller.
func New(registry Registry) *Controller {
	return &Controller{g2: registry}
}

// title is what the commit and the pull request say.
const title = "feat: move every package onto the default branch"

// Consolidate opens the pull request that moves every package onto the default branch.
//
// Running it again is safe: an entry naming the object already at its path changes nothing,
// and the head branch is written again from the default branch's tip.
func (c *Controller) Consolidate(ctx context.Context, logger *slog.Logger, dryRun bool) error {
	pkgs, err := c.g2.LegacyPackages(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the error says what it couldn't read
	}
	entries := g2.ConsolidatedEntries(pkgs)
	withoutVersions := 0
	for _, pkg := range pkgs {
		if !holdsVersions(pkg) {
			withoutVersions++
		}
	}
	logger.Info("read the package branches",
		"num_of_packages", len(pkgs), "num_of_entries", len(entries), "num_of_packages_without_versions", withoutVersions)
	if dryRun || len(entries) == 0 {
		return nil
	}
	parent, err := c.g2.BranchSHA(ctx, g2.DefaultBranch)
	if err != nil {
		return err //nolint:wrapcheck
	}
	if err := c.g2.CommitEntries(ctx, g2.ConsolidateBranch, parent, title, entries); err != nil {
		return fmt.Errorf("commit the packages: %w", err)
	}
	pr, err := c.g2.CreatePullRequestFrom(ctx, logger, g2.ConsolidateBranch, g2.DefaultBranch, title, body(pkgs))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened the pull request that moves the packages", "number", pr.GetNumber())
	return nil
}

// holdsVersions reports whether the branch holds a versions directory.
func holdsVersions(pkg *g2.LegacyPackage) bool {
	for _, entry := range pkg.Entries {
		if entry.GetPath() == "versions" {
			return true
		}
	}
	return false
}

// body says what the pull request does and how to check it.
func body(pkgs []*g2.LegacyPackage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Moves the %d packages kept on `pkg_<id>` branches onto the default branch, under "+
		"`pkgs/<last two digits of id>/<id>/` (#695).\n\n", len(pkgs))
	b.WriteString("Each package's `registry.yaml`, `versions.json` and `versions/` are the objects its branch " +
		"holds, named by their sha rather than uploaded again. What every version serves is the same bytes, " +
		"and `versions.json`'s `source` is still the sha of the directory it lists.\n\n")
	b.WriteString("It can be checked without reading the diff, which GitHub shows only the start of: for each " +
		"branch, `git rev-parse pkg_<id>:versions` equals `git rev-parse <this head>:pkgs/<shard>/<id>/versions`, " +
		"and the same for the two files.\n\n")
	b.WriteString("The branches stay as they are. Nothing writes to them from here on.\n")
	return b.String()
}
