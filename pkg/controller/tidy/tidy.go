// Package tidy takes out of the definitions on the package branches what a release can be
// read for.
//
// The conversion stopped writing it in, so a definition written since says only the part a
// release can't answer. The ones written before still say the rest, and nothing else would
// ever reach them: a definition is read on every generation and rewritten on none.
package tidy

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/tidy"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// Registry is what a tidy reads and writes.
type Registry interface {
	File(ctx context.Context, ref, path string) (string, error)
	PackagesInFlight(ctx context.Context) (map[string]struct{}, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	CreatePullRequest(ctx context.Context, pkgName, title, body string) (*gogithub.PullRequest, error)
}

// Definitions reads every package branch's definition in one pass.
type Definitions interface {
	Files(ctx context.Context, logger *slog.Logger, prefix, path string) (map[string]string, error)
}

// AutoMerger turns a pull request over to its checks.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// Controller tidies definitions.
type Controller struct {
	g2        Registry
	defs      Definitions
	automerge AutoMerger
}

// New creates a Controller. automerge may be nil, and then the pull requests wait for
// someone.
func New(registry Registry, defs Definitions, automerge AutoMerger) *Controller {
	return &Controller{g2: registry, defs: defs, automerge: automerge}
}

// Args are the command's own options.
type Args struct {
	// Packages limits the tidy to the ones named. Empty is every package the registry
	// holds a definition of.
	Packages []string
	// Limit bounds how many pull requests one run opens. A pull request per package is
	// what one branch per package makes of a change to them all.
	Limit int
	// DryRun says what would change and writes nothing.
	DryRun bool
}

// Tidy takes what a release can be read for out of each definition that still says it.
func (c *Controller) Tidy(ctx context.Context, logger *slog.Logger, args *Args) error {
	definitions, err := c.definitions(ctx, logger, args)
	if err != nil {
		return err
	}
	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return fmt.Errorf("list the packages with an open pull request: %w", err)
	}

	opened := 0
	for _, pkgName := range slices.Sorted(maps.Keys(definitions)) {
		if args.Limit > 0 && opened >= args.Limit {
			logger.Info("the run has opened as many pull requests as it may",
				"limit", args.Limit)
			return nil
		}
		tidied, err := c.tidyPackage(ctx, logger, pkgName, definitions[pkgName], inFlight, args)
		if err != nil {
			// One package is one pull request, and the others are still worth opening.
			slogerr.WithError(logger, err).Warn("failed to tidy the definition", "package", pkgName)
			continue
		}
		if tidied {
			opened++
		}
	}
	logger.Info("tidied the definitions", "num_of_packages", opened)
	return nil
}

// definitions is what the run works through, by package name.
func (c *Controller) definitions(ctx context.Context, logger *slog.Logger, args *Args) (map[string]string, error) {
	if len(args.Packages) == 0 {
		files, err := c.defs.Files(ctx, logger, aquag2.BranchPrefix, g2.ConfigFileName)
		if err != nil {
			return nil, fmt.Errorf("read the definitions of the package branches: %w", err)
		}
		out := make(map[string]string, len(files))
		for branch, content := range files {
			if pkgName, ok := aquag2.PackageName(branch); ok {
				out[pkgName] = content
			}
		}
		logger.Info("read the definitions on the package branches", "num_of_definitions", len(out))
		return out, nil
	}

	out := make(map[string]string, len(args.Packages))
	for _, pkgName := range args.Packages {
		content, err := c.g2.File(ctx, aquag2.BranchName(pkgName), g2.ConfigFileName)
		if err != nil {
			return nil, fmt.Errorf("read the definition of %s: %w", pkgName, err)
		}
		if content == "" {
			return nil, fmt.Errorf("%w: %s", errNoDefinition, pkgName)
		}
		out[pkgName] = content
	}
	return out, nil
}

// tidyPackage opens the pull request for one definition, and says whether it did.
func (c *Controller) tidyPackage(ctx context.Context, logger *slog.Logger, pkgName, content string, inFlight map[string]struct{}, args *Args) (bool, error) {
	logger = logger.With("package", pkgName)
	tidied, removed, err := tidy.Replacements(content)
	if err != nil {
		return false, fmt.Errorf("tidy the definition: %w", err)
	}
	if len(removed) == 0 {
		logger.Debug("the definition says nothing a release is read for")
		return false, nil
	}
	logger.Info("the definition says what a release is read for", "removed", strings.Join(removed, ", "))
	if args.DryRun {
		return false, nil
	}
	if _, ok := inFlight[g2.HeadBranchName(pkgName)]; ok {
		// Committing would reset the branch that pull request is on, and what is waiting
		// there is versions somebody may be reading.
		logger.Info("the package has an open pull request, so its definition waits")
		return false, nil
	}
	return true, c.openPullRequest(ctx, logger, pkgName, tidied, removed)
}

// openPullRequest commits the tidied definition and takes it to a pull request.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, pkgName, content string, removed []string) error {
	base, err := c.g2.BranchSHA(ctx, g2.BranchName(pkgName))
	if err != nil {
		return fmt.Errorf("get the package branch: %w", err)
	}
	if base == "" {
		return fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}

	title := fmt.Sprintf("chore(%s): drop the replacements a release is read for", pkgName)
	files := []*g2.File{{Path: g2.ConfigFileName, Content: content}}
	if err := c.g2.Commit(ctx, g2.HeadBranchName(pkgName), base, title, files); err != nil {
		return fmt.Errorf("commit the definition: %w", err)
	}
	pr, err := c.g2.CreatePullRequest(ctx, pkgName, title, body(removed))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened a pull request", "number", pr.GetNumber())

	if c.automerge == nil {
		return nil
	}
	if err := c.automerge.EnableAutoMerge(ctx, pr.GetNodeID()); err != nil {
		// The pull request is open and correct; it just waits for someone.
		slogerr.WithError(logger, err).Warn("failed to turn on auto-merge", "number", pr.GetNumber())
	}
	return nil
}

// body says what went and why nothing follows from it.
func body(removed []string) string {
	var b strings.Builder
	b.WriteString("Generated by `ar2 tidy`.\n\nThese say how the release writes a platform, " +
		"and the parser reads every one of them off the asset names for itself:\n\n")
	for _, line := range removed {
		b.WriteString("- `" + line + "`\n")
	}
	b.WriteString("\nSo the definition was saying something that can be looked at, which is a copy " +
		"that can go out of date while nobody touches it: a spelling a release stops using leaves " +
		"the definition insisting on it.\n")
	b.WriteString("\nNothing generated changes. The conversion has written definitions this way " +
		"since ar2 v0.0.27, and a package whose definition declared five of them was measured as " +
		"generating the same bytes either way. A spelling the parser can't read -- `linux: ubuntu`, " +
		"for luau -- is not here: it is the only thing that makes such an asset belong to a " +
		"platform, and it stays.\n")
	return b.String()
}
