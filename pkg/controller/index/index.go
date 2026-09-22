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
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
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

// AddPackage puts one package into the catalogue.
//
// This is the path a run takes as it takes a package over: it has just built the
// definition, so nothing is read back and no branches are listed. The definition is
// passed in because the branch doesn't hold it yet — it is in the pull request that
// brings the package, which is the moment the catalogue can first describe it.
func (c *Controller) AddPackage(ctx context.Context, logger *slog.Logger, pkgName string, cfg *aquag2.Config) error {
	return c.addOne(ctx, logger, pkgName, cfg)
}

// Sync puts every package that has a branch into the catalogue.
//
// This is what catches what the other path missed. A package whose run failed after
// committing its definition, or whose pull request was never merged, is left out of
// the catalogue and nothing else would ever notice.
func (c *Controller) Sync(ctx context.Context, logger *slog.Logger) error {
	pkgNames, err := c.g2.PackageBranches(ctx)
	if err != nil {
		return fmt.Errorf("list the package branches: %w", err)
	}
	logger.Info("listed the package branches", "num_of_packages", len(pkgNames))
	return c.add(ctx, logger, pkgNames)
}
