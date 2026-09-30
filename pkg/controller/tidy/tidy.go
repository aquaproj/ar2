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
	// Branch and HeadBranch are the branch holding the package and the branch a pull
	// request for it is opened from. A branch is named after the package's id, so they
	// answer false for a package the registry doesn't hold.
	Branch(pkgName string) (string, bool)
	HeadBranch(pkgName string) (string, bool)
	CreatePullRequest(ctx context.Context, logger *slog.Logger, pkgName, title, body string) (*gogithub.PullRequest, error)
}

// AutoMerger turns a pull request over to its checks.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// Controller tidies definitions.
type Controller struct {
	g2 Registry
	// defs is the definition on every package branch, which is also what the identities
	// were read out of.
	defs      map[string]string
	automerge AutoMerger
}

// New creates a Controller. automerge may be nil, and then the pull requests wait for
// someone.
func New(registry Registry, defs map[string]string, automerge AutoMerger) *Controller {
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
		files := c.defs
		out := make(map[string]string, len(files))
		ids := g2.NewIdentities(logger, files)
		for branch, content := range files {
			id, ok := g2.BranchID(branch)
			if !ok {
				// A branch still named after the package, which nothing writes to
				// any more.
				continue
			}
			pkgName, ok := ids.Package(id)
			if !ok {
				// Nothing on the branch says which package it holds, so there is no
				// package to open a pull request for.
				continue
			}
			out[pkgName] = content
		}
		logger.Info("read the definitions on the package branches", "num_of_definitions", len(out))
		return out, nil
	}

	out := make(map[string]string, len(args.Packages))
	for _, pkgName := range args.Packages {
		branch, ok := c.g2.Branch(pkgName)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errNoBranch, pkgName)
		}
		content, err := c.g2.File(ctx, branch, g2.ConfigFileName)
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
	tidied, removed, err := tidy.Definition(content)
	if err != nil {
		return false, fmt.Errorf("tidy the definition: %w", err)
	}
	if !removed.Any() {
		logger.Debug("the definition says nothing it doesn't have to")
		return false, nil
	}
	logger.Info("the definition says what it doesn't have to",
		"replacements", strings.Join(removed.Spellings, ", "),
		"checksum_blocks", removed.Checksums, "empty_overrides", removed.Overrides)
	if args.DryRun {
		return false, nil
	}
	head, ok := c.g2.HeadBranch(pkgName)
	if !ok {
		return false, fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}
	if _, inFlight := inFlight[head]; inFlight {
		// Committing would reset the branch that pull request is on, and what is waiting
		// there is versions somebody may be reading.
		logger.Info("the package has an open pull request, so its definition waits")
		return false, nil
	}
	return true, c.openPullRequest(ctx, logger, pkgName, tidied, removed)
}

// openPullRequest commits the tidied definition and takes it to a pull request.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, pkgName, content string, removed *tidy.Removed) error {
	branch, ok := c.g2.Branch(pkgName)
	if !ok {
		return fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}
	head, _ := c.g2.HeadBranch(pkgName)
	base, err := c.g2.BranchSHA(ctx, branch)
	if err != nil {
		return fmt.Errorf("get the package branch: %w", err)
	}
	if base == "" {
		return fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}

	title := fmt.Sprintf("chore(%s): %s", pkgName, what(removed))
	files := []*g2.File{{Path: g2.ConfigFileName, Content: content}}
	if err := c.g2.Commit(ctx, head, base, title, files); err != nil {
		return fmt.Errorf("commit the definition: %w", err)
	}
	pr, err := c.g2.CreatePullRequest(ctx, logger, pkgName, title, body(removed))
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

// what names the tidying, for the title. The two reasons are different enough to be
// worth saying apart: one thing is read off the release every time, the other is read by
// nobody at all.
func what(removed *tidy.Removed) string {
	switch {
	case removed.Checksums == 0 && removed.Overrides == 0:
		return "drop the replacements a release is read for"
	case len(removed.Spellings) == 0 && removed.Overrides == 0:
		return "drop the checksum file nothing reads"
	case len(removed.Spellings) == 0 && removed.Checksums == 0:
		return "drop the overrides that say nothing"
	default:
		return "drop what the definition doesn't have to say"
	}
}

// body says what went and why nothing follows from it.
func body(removed *tidy.Removed) string {
	var b strings.Builder
	b.WriteString("Generated by `ar2 tidy`.\n")
	if len(removed.Spellings) > 0 {
		b.WriteString("\nThese say how the release writes a platform, and the parser reads " +
			"every one of them off the asset names for itself:\n\n")
		for _, line := range removed.Spellings {
			b.WriteString("- `" + line + "`\n")
		}
		b.WriteString("\nSo the definition was saying something that can be looked at, which is a copy " +
			"that can go out of date while nobody touches it: a spelling a release stops using leaves " +
			"the definition insisting on it.\n")
		b.WriteString("\nThe conversion has written definitions this way since ar2 v0.0.27, and a " +
			"package whose definition declared five of them was measured as generating the same bytes " +
			"either way. A spelling the parser can't read -- `linux: ubuntu`, for luau -- is not here: " +
			"it is the only thing that makes such an asset belong to a platform, and it stays.\n")
	}
	if removed.Checksums > 0 {
		fmt.Fprintf(&b, "\n%s said where the checksum file is, and how its signature is "+
			"checked. aqua-registry verifies an asset against that file; here every entry carries the "+
			"digest of the asset itself, taken when the entry was generated and checked again by this "+
			"branch's CI on a machine of the environment the entry is for. Nothing reads the file, or "+
			"even knows it exists, so the definition was saying it to nobody.\n",
			blocks(removed.Checksums))
	}
	if removed.Overrides > 0 {
		fmt.Fprintf(&b, "\n%s said which environment it was for and nothing else, so it matched "+
			"that environment and then applied nothing to it. An override with a later sibling the "+
			"same environment could match stays, because the first match is the one applied, and so "+
			"does one carrying variants: naming a variant is what makes an entry for it generated.\n",
			overrides(removed.Overrides))
	}
	b.WriteString("\nNothing generated changes.\n")
	return b.String()
}

// blocks names how many checksum blocks went, for a sentence that reads either way.
func blocks(n int) string {
	if n == 1 {
		return "One block"
	}
	return fmt.Sprintf("%d blocks", n)
}

// overrides names how many overrides went, for a sentence that reads either way.
func overrides(n int) string {
	if n == 1 {
		return "One override"
	}
	return fmt.Sprintf("%d overrides", n)
}
