// Package index maintains aqua-registry-g2's catalogue of the packages it holds.
//
// Everything else about a package lives on its own branch, which is what keeps one
// package's history out of another's. Searching can't work that way: it reads a name
// and a description for every package before knowing which one is wanted. So the
// catalogue is a single file on the default branch, and it has to be kept in step
// with the branches by hand.
package index

import (
	"context"
	"fmt"
	"log/slog"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/szksh-lab-2/ar2/pkg/g2"
)

// Registry is what the catalogue is built from and written to.
type Registry interface {
	Index(ctx context.Context, ref string) (*aquag2.Index, error)
	PackageBranches(ctx context.Context) ([]string, error)
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	IndexPullRequest(ctx context.Context) (*gogithub.PullRequest, error)
	CreateIndexPullRequest(ctx context.Context, base, title, body string) (*gogithub.PullRequest, error)
}

// Controller adds packages to the catalogue.
type Controller struct {
	g2         Registry
	baseBranch string
}

// New creates a Controller.
func New(registry Registry, baseBranch string) *Controller {
	return &Controller{g2: registry, baseBranch: baseBranch}
}

// Add puts one package into the catalogue.
//
// This is the path a new branch takes. The branch that was just created is the only
// one that can be missing, so nothing else is looked at: no branches are listed and
// no other definition is read.
func (c *Controller) Add(ctx context.Context, logger *slog.Logger, branch string) error {
	pkgName, ok := aquag2.PackageName(branch)
	if !ok {
		return fmt.Errorf("%w: %s", errNotAPackageBranch, branch)
	}
	return c.add(ctx, logger, []string{pkgName})
}

// Sync puts every package that has a branch into the catalogue.
//
// This is what catches what the other path missed. A branch created while the event
// didn't fire, or whose pull request failed or was never merged, leaves a package out
// of the catalogue and nothing else would ever notice.
func (c *Controller) Sync(ctx context.Context, logger *slog.Logger) error {
	pkgNames, err := c.g2.PackageBranches(ctx)
	if err != nil {
		return fmt.Errorf("list the package branches: %w", err)
	}
	logger.Info("listed the package branches", "num_of_packages", len(pkgNames))
	return c.add(ctx, logger, pkgNames)
}
