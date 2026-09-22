package index

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// target is the catalogue an update is built on top of, and where it came from.
type target struct {
	pr    *gogithub.PullRequest
	index *aquag2.Index
	ref   string
}

// read fetches the catalogue the update is added to.
//
// It is read from the branch the update is written to rather than from the default
// branch, so that a run adds to what an earlier one is still waiting to merge.
// Reading the default branch instead would make the second run undo the first.
func (c *Controller) read(ctx context.Context) (*target, error) {
	pr, err := c.g2.IndexPullRequest(ctx)
	if err != nil {
		return nil, fmt.Errorf("look for an open pull request: %w", err)
	}
	ref := c.baseBranch
	if pr != nil {
		ref = g2.IndexBranch
	}
	index, err := c.g2.Index(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	return &target{pr: pr, index: index, ref: ref}, nil
}

// add puts the packages the catalogue doesn't have into it, reading a definition for
// each one that is missing.
func (c *Controller) add(ctx context.Context, logger *slog.Logger, pkgNames []string) error {
	t, err := c.read(ctx)
	if err != nil {
		return err
	}
	added, err := c.entries(ctx, logger, t.index, pkgNames)
	if err != nil {
		return err
	}
	return c.write(ctx, logger, t, added)
}

// addOne puts one package into the catalogue from a definition already in hand.
func (c *Controller) addOne(ctx context.Context, logger *slog.Logger, pkgName string, cfg *aquag2.Config) error {
	t, err := c.read(ctx)
	if err != nil {
		return err
	}
	if _, ok := t.index.Names()[pkgName]; ok {
		logger.Debug("the catalogue already holds the package", "package", pkgName)
		return nil
	}
	logger.Info("adding a package to the catalogue", "package", pkgName)
	return c.write(ctx, logger, t, []*aquag2.IndexPackage{aquag2.NewIndexPackage(pkgName, cfg)})
}

// write commits the catalogue and takes it to a pull request.
func (c *Controller) write(ctx context.Context, logger *slog.Logger, t *target, added []*aquag2.IndexPackage) error {
	if len(added) == 0 {
		logger.Info("the catalogue is up to date")
		return nil
	}
	t.index.Add(added...)

	if err := c.commit(ctx, logger, t.index, t.ref, added); err != nil {
		return err
	}
	if t.pr != nil {
		logger.Info("added to the open pull request",
			"number", t.pr.GetNumber(), "num_of_packages", len(added))
		return nil
	}
	return c.openPullRequest(ctx, logger, added)
}

// entries reads the definition of each package the catalogue is missing.
//
// Only the missing ones: the definition is a request per package, and a run that
// found nothing missing makes none at all.
func (c *Controller) entries(ctx context.Context, logger *slog.Logger, index *aquag2.Index, pkgNames []string) ([]*aquag2.IndexPackage, error) {
	have := index.Names()
	added := make([]*aquag2.IndexPackage, 0, len(pkgNames))
	for _, pkgName := range pkgNames {
		if _, ok := have[pkgName]; ok {
			continue
		}
		cfg, err := c.g2.Config(ctx, pkgName)
		if err != nil {
			return nil, fmt.Errorf("get the definition of %s: %w", pkgName, err)
		}
		if cfg == nil {
			// The branch exists but nothing has been generated onto it yet. There
			// is nothing to describe the package with, and the run that writes its
			// definition will bring it here.
			logger.Debug("the package has no definition yet", "package", pkgName)
			continue
		}
		logger.Info("adding a package to the catalogue", "package", pkgName)
		added = append(added, aquag2.NewIndexPackage(pkgName, cfg))
	}
	return added, nil
}

func (c *Controller) commit(ctx context.Context, logger *slog.Logger, index *aquag2.Index, ref string, added []*aquag2.IndexPackage) error {
	content, err := index.Marshal()
	if err != nil {
		return err //nolint:wrapcheck
	}
	parent, err := c.g2.BranchSHA(ctx, ref)
	if err != nil {
		return fmt.Errorf("get the branch to commit onto: %w", err)
	}
	logger.Debug("committing the catalogue", "branch", g2.IndexBranch, "parent", parent)
	if err := c.g2.Commit(ctx, g2.IndexBranch, parent, commitMessage(added), []*g2.File{{
		Path:    g2.IndexFileName,
		Content: content,
	}}); err != nil {
		return fmt.Errorf("commit the catalogue: %w", err)
	}
	return nil
}

func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, added []*aquag2.IndexPackage) error {
	pr, err := c.g2.CreateIndexPullRequest(ctx, c.baseBranch, commitMessage(added), prBody(added))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened a pull request",
		"number", pr.GetNumber(), "num_of_packages", len(added))
	return nil
}

func commitMessage(added []*aquag2.IndexPackage) string {
	if len(added) == 1 {
		return fmt.Sprintf("feat(%s): add the package to the index", added[0].Name)
	}
	return fmt.Sprintf("feat: add %d packages to the index", len(added))
}

func prBody(added []*aquag2.IndexPackage) string {
	var body strings.Builder
	body.WriteString("Generated by ar2.\n\n")
	for _, pkg := range added {
		body.WriteString("- " + pkg.Name + "\n")
	}
	return body.String()
}
