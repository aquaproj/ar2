package index

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
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
	return c.write(ctx, logger, t, &change{packages: added})
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
	return c.write(ctx, logger, t, &change{
		packages: []*aquag2.IndexPackage{aquag2.NewIndexPackage(pkgName, cfg)},
	})
}

// write commits the catalogue and takes it to a pull request.
//
// What is committed is whichever of its files the repository doesn't already hold as
// they are rendered, rather than whichever packages were added. The two are usually the
// same, and aren't when a file is added to what a run maintains: the packages were all
// there already and one of the files was never written, which nothing else would notice.
func (c *Controller) write(ctx context.Context, logger *slog.Logger, t *target, change *change) error {
	t.index.Add(change.packages...)
	files, err := c.differing(ctx, t)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		logger.Info("the catalogue is up to date")
		return nil
	}

	if err := c.commit(ctx, logger, t.ref, files, change); err != nil {
		return err
	}
	if t.pr != nil {
		logger.Info("added to the open pull request",
			"number", t.pr.GetNumber(), "num_of_packages", len(change.packages))
		return nil
	}
	return c.openPullRequest(ctx, logger, change)
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

func (c *Controller) commit(ctx context.Context, logger *slog.Logger, ref string, files []*g2.File, change *change) error {
	parent, err := c.g2.BranchSHA(ctx, ref)
	if err != nil {
		return fmt.Errorf("get the branch to commit onto: %w", err)
	}
	logger.Debug("committing the catalogue", "branch", g2.IndexBranch, "parent", parent)
	if err := c.g2.Commit(ctx, g2.IndexBranch, parent, commitMessage(change), files); err != nil {
		return fmt.Errorf("commit the catalogue: %w", err)
	}
	return nil
}

// differing renders the catalogue's files and returns the ones the repository doesn't
// already hold as rendered.
//
// The comparison is on the bytes rather than on what changed, so a file the registry was
// never written with is committed the first time a run reaches it, and one that is
// already right is not committed again.
func (c *Controller) differing(ctx context.Context, t *target) ([]*g2.File, error) {
	files, err := catalogue(t.index)
	if err != nil {
		return nil, err
	}
	out := make([]*g2.File, 0, len(files))
	for _, file := range files {
		held, err := c.g2.File(ctx, t.ref, file.Path)
		if err != nil {
			return nil, err //nolint:wrapcheck
		}
		if held == file.Content {
			continue
		}
		out = append(out, file)
	}
	return out, nil
}

// catalogue renders the files the catalogue is made of.
//
// The table of other names is written with it rather than beside it. It holds the same
// aliases the catalogue does, inverted into something a name can be looked up in, so
// writing them in one commit is what keeps them from describing different registries.
func catalogue(index *aquag2.Index) ([]*g2.File, error) {
	indexContent, err := index.Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	aliasContent, err := aquag2.NewAliases(index).Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	return []*g2.File{
		{Path: g2.IndexFileName, Content: indexContent},
		{Path: aquag2.AliasesFileName, Content: aliasContent},
	}, nil
}

func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, change *change) error {
	pr, err := c.g2.CreateIndexPullRequest(ctx, c.baseBranch, commitMessage(change), prBody(change))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened a pull request",
		"number", pr.GetNumber(), "num_of_packages", len(change.packages))

	// The catalogue is a name, a description and a link per package, read out of
	// definitions that were reviewed when they arrived. There is nothing here for a
	// person to decide, so it goes the way a package version does: the checks on main
	// say whether it is a file aqua can read, and it merges itself when they pass.
	if c.automerge == nil {
		return nil
	}
	if err := c.automerge.EnableAutoMerge(ctx, pr.GetNodeID()); err != nil {
		// The pull request is open and correct; it just waits for someone.
		slogerr.WithError(logger, err).Warn("failed to turn on auto-merge",
			"number", pr.GetNumber())
	}
	return nil
}

// change is what one update does to the catalogue.
//
// Whether the entries are new or read again is not something the entries themselves say,
// and it is the whole difference between the two sentences a commit message can be.
type change struct {
	packages []*aquag2.IndexPackage
	// refreshed says the entries replace ones the catalogue already had, because a
	// definition on a package branch was edited after the entry was made from it.
	refreshed bool
}

func commitMessage(change *change) string {
	if change.refreshed {
		if len(change.packages) == 1 {
			return fmt.Sprintf("fix(%s): update the index entry", change.packages[0].Name)
		}
		return fmt.Sprintf("fix: update the index entries of %d packages", len(change.packages))
	}
	switch len(change.packages) {
	case 0:
		// The packages were all there and a file of the catalogue wasn't.
		return "chore: write the catalogue's files as they are rendered now"
	case 1:
		return fmt.Sprintf("feat(%s): add the package to the index", change.packages[0].Name)
	default:
		return fmt.Sprintf("feat: add %d packages to the index", len(change.packages))
	}
}

func prBody(change *change) string {
	var body strings.Builder
	body.WriteString("Generated by ar2.\n\n")
	if len(change.packages) == 0 {
		body.WriteString("No package was added. A file of the catalogue wasn't what it is " +
			"rendered as, which is what a file added to what a run maintains looks like " +
			"until a run reaches it.\n")
		return body.String()
	}
	for _, pkg := range change.packages {
		body.WriteString("- " + pkg.Name + "\n")
	}
	if change.refreshed {
		body.WriteString("\nThe entries were read out of the definitions on the packages' branches " +
			"again, because a definition is edited after the entry was made from it. What an entry " +
			"holds is the definition's description, link, search words and aliases, so this is what " +
			"the catalogue and the table of other names beside it say about these packages now.\n")
	}
	return body.String()
}
