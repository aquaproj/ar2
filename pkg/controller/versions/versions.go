// Package versions writes the list of versions a package branch holds.
//
// The list is the branch's own directories, so nothing here decides what it says. What it
// is for is reading: a reader asking which versions there are, when each was published or
// whether the file it has is still the one the registry serves would otherwise read the
// branch's tree and then a file per version.
//
// It is written onto the package branch, beside the definition, because that is where the
// versions are. A copy on the default branch would be one file every package writes to.
package versions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
)

// Registry is the part of aqua-registry-g2 this reads and writes.
type Registry interface {
	Branch(pkgName string) (string, bool)
	BranchSHA(ctx context.Context, branch string) (string, error)
	VersionsTree(ctx context.Context, ref string) (string, error)
	VersionsOnRef(ctx context.Context, logger *slog.Logger, ref string) (map[string]struct{}, error)
	File(ctx context.Context, ref, path string) (string, error)
	Push(ctx context.Context, branch, parent, message string, files []*g2.File) error
}

// Controller writes the lists.
type Controller struct {
	registry Registry
	// defs is each package branch's definition, keyed by branch name, which is what says
	// which package a branch holds.
	defs map[string]string
}

// New creates a Controller.
func New(registry Registry, defs map[string]string) *Controller {
	return &Controller{registry: registry, defs: defs}
}

// Args holds what one run does.
type Args struct {
	// Packages narrows the run to the packages named. Naming none does every package.
	Packages []string
	// Branches narrows the run to the branches named, which is what a branch's own
	// workflow passes: it knows the branch it is running on and not the package.
	Branches []string
	// Limit bounds how many branches one run writes to, 0 for as many as there are.
	Limit int
	// DryRun says what would be written without writing it.
	DryRun bool
}

// Write works through the branches, writing the list of each whose list has changed.
//
// A branch whose list is already what it should be is left alone, so a run over a registry
// that is up to date writes nothing.
func (c *Controller) Write(ctx context.Context, logger *slog.Logger, args *Args) error {
	written := 0
	for _, branch := range c.branches(logger, args) {
		if args.Limit > 0 && written >= args.Limit {
			logger.Info("stopping at the limit", "limit", args.Limit)
			return nil
		}
		wrote, err := c.write(ctx, logger.With("branch", branch), branch, args.DryRun)
		if err != nil {
			// One branch failing says nothing about the others, and a list that wasn't
			// written is a list the next run writes.
			logger.Warn("failed to write the versions", "branch", branch, "error", err.Error())
			continue
		}
		if wrote {
			written++
		}
	}
	return nil
}

// branches is what the run works through, in name order so that two runs of the same
// registry do the same thing in the same order.
func (c *Controller) branches(logger *slog.Logger, args *Args) []string {
	if len(args.Branches) > 0 {
		return packageBranches(logger, args.Branches)
	}
	if len(args.Packages) > 0 {
		out := make([]string, 0, len(args.Packages))
		for _, pkgName := range args.Packages {
			branch, ok := c.registry.Branch(pkgName)
			if !ok {
				logger.Warn("the registry holds no branch for this package", "package", pkgName)
				continue
			}
			out = append(out, branch)
		}
		return out
	}
	out := make([]string, 0, len(c.defs))
	for branch := range c.defs {
		out = append(out, branch)
	}
	slices.Sort(out)
	return out
}

// packageBranches is the named branches that are package branches.
//
// What a list is of is the versions a package branch holds. The head branch of a pull
// request is cut from one, so it holds the versions too and anything watching a package
// branch watches its pull requests as well -- and writing a list onto one would be writing
// into somebody's pull request. The branches are told apart by their names: a package
// branch is named after an id.
func packageBranches(logger *slog.Logger, branches []string) []string {
	out := make([]string, 0, len(branches))
	for _, branch := range branches {
		if _, ok := g2.BranchID(branch); !ok {
			logger.Info("not a package branch, so it holds no list to write", "branch", branch)
			continue
		}
		out = append(out, branch)
	}
	return out
}

// write writes one branch's list, and reports whether anything was written.
//
// What the list is of is the versions directory, so the directory's sha is what says
// whether the list is still it. The list records the sha it was made from, and a branch
// whose directory is still that one is answered for by two requests: the sha, and the list
// that names it. Everything else -- the versions, and a file per version -- is read only
// when something has changed.
func (c *Controller) write(ctx context.Context, logger *slog.Logger, branch string, dryRun bool) (bool, error) {
	tree, err := c.registry.VersionsTree(ctx, branch)
	if err != nil {
		return false, err //nolint:wrapcheck // the error names the branch
	}
	if tree == "" {
		// A branch holding no versions. There is no list to write, and writing an empty
		// one would say the registry holds nothing of a package it has just taken over.
		return false, nil
	}
	held, err := c.registry.File(ctx, branch, aquag2.VersionsFileName)
	if err != nil {
		return false, fmt.Errorf("read the list the branch holds: %w", err)
	}
	if source(held) == tree {
		logger.Debug("the list is of the versions the branch holds")
		return false, nil
	}

	versions, err := c.list(ctx, logger, branch, tree)
	if err != nil {
		return false, err
	}
	if len(versions.Versions) == 0 {
		// A versions directory holding nothing this reads as a version. Writing the list
		// would say the registry holds no version of the package, where what is true is
		// that this couldn't find one.
		logger.Warn("the versions directory holds no version")
		return false, nil
	}
	content, err := marshal(versions)
	if err != nil {
		return false, err
	}
	if content == held {
		return false, nil
	}
	logger.Info("writing the versions", "versions", len(versions.Versions))
	if dryRun {
		return true, nil
	}
	return true, c.push(ctx, branch, content, len(versions.Versions))
}

// push writes the list onto the package branch.
func (c *Controller) push(ctx context.Context, branch, content string, versions int) error {
	sha, err := c.registry.BranchSHA(ctx, branch)
	if err != nil {
		return err //nolint:wrapcheck // the error names the branch
	}
	if sha == "" {
		return nil
	}
	files := []*g2.File{{Path: aquag2.VersionsFileName, Content: content}}
	if err := c.registry.Push(ctx, branch, sha, message(versions), files); err != nil {
		return fmt.Errorf("push the versions onto the package branch: %w", err)
	}
	return nil
}

// source is the versions directory the list the branch holds was made from, and empty when
// the branch holds no list or one that can't be read.
func source(held string) string {
	if held == "" {
		return ""
	}
	versions, err := aquag2.ReadVersions(strings.NewReader(held))
	if err != nil {
		return ""
	}
	return versions.Source
}

// list is what the branch holds, read from the branch.
func (c *Controller) list(ctx context.Context, logger *slog.Logger, branch, tree string) (*aquag2.Versions, error) {
	held, err := c.registry.VersionsOnRef(ctx, logger, branch)
	if err != nil {
		return nil, fmt.Errorf("list the versions the branch holds: %w", err)
	}
	out := &aquag2.Versions{Source: tree, Versions: make([]*aquag2.Version, 0, len(held))}
	for version := range held {
		entry, err := c.entry(ctx, logger, branch, version)
		if err != nil {
			return nil, err
		}
		out.Versions = append(out.Versions, entry)
	}
	out.Sort()
	return out, nil
}

// entry is what the list says about one version, read from the file the registry serves
// for it.
func (c *Controller) entry(ctx context.Context, logger *slog.Logger, branch, version string) (*aquag2.Version, error) {
	content, err := c.registry.File(ctx, branch, aquag2.Path(version))
	if err != nil {
		return nil, fmt.Errorf("read the registry.json of %s: %w", version, err)
	}
	entry := &aquag2.Version{Version: version}
	if content == "" {
		// The directory is there and the file isn't, which is a version the registry
		// doesn't serve. It is still a version the branch holds, so it is listed, and
		// what can't be read about it is left out.
		logger.Warn("the version holds no registry.json", "version", version)
		return entry, nil
	}
	entry.Digest = digest(content)
	reg := &aquag2.Registry{}
	if err := json.Unmarshal([]byte(content), reg); err != nil {
		logger.Warn("failed to read a registry.json", "version", version, "error", err.Error())
		return entry, nil
	}
	entry.PublishedAt = reg.PublishedAt
	return entry, nil
}

// digest is the SHA-256 of the file the registry serves, as a reader of the list would
// compute it over the bytes it downloaded.
func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// marshal renders the list indented. It is read by people in a pull request as well as by
// programs, it is one file per package rather than per version, and git packs the
// whitespace away.
func marshal(versions *aquag2.Versions) (string, error) {
	b, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", aquag2.VersionsFileName, err)
	}
	return string(b) + "\n", nil
}

// message is what the commit says.
func message(versions int) string {
	if versions == 1 {
		return "chore: list the version the registry holds"
	}
	return fmt.Sprintf("chore: list the %d versions the registry holds", versions)
}
