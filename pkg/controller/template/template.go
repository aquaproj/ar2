// Package template brings the package branches' copies of the template up to date.
//
// A package branch is created holding the files in the template, and a pull_request or push
// workflow has to be on the branch it runs for, which is why those files are there rather
// than on main. They are copied once and never updated, so what they hold is a call into
// main, where it can be improved for every package at once.
//
// What that leaves is a file added to the template afterwards, which the branches created
// before it don't have, and a file whose call has to change. Both are this: the template
// says what a branch should hold, and a branch holding something else is written to.
//
// One way, and nothing is deleted. The template names the paths it answers for and the rest
// of the branch is the branch's own -- the definition, the versions, the list of them -- so
// what isn't named is not touched. A file taken out of the template therefore stays where it
// was copied, and taking it off the branches is a separate thing to decide: the rule it
// would need is which paths the template owns rather than which files it has, since the
// branch holds plenty the template never did.
package template

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// Registry is the part of aqua-registry-g2 this reads and writes.
type Registry interface {
	Template(ctx context.Context) (map[string]*gogithub.TreeEntry, error)
	PackageBranches(ctx context.Context) ([]*g2.Branch, error)
	BlobSHAs(ctx context.Context, ref string) (map[string]string, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	PushEntries(ctx context.Context, branch, parent, message string, entries []*gogithub.TreeEntry) error
}

// Controller writes the template onto the branches.
type Controller struct {
	registry Registry
}

// New creates a Controller.
func New(registry Registry) *Controller {
	return &Controller{registry: registry}
}

// Args holds what one run does.
type Args struct {
	// Branches narrows the run to the branches named. Naming none does every package
	// branch.
	Branches []string
	// Limit bounds how many branches one run writes to, 0 for as many as there are.
	Limit int
	// DryRun says what would be written without writing it.
	DryRun bool
}

// Sync writes the template onto every branch that holds something else.
//
// A branch holding what the template says costs the one request that read it, so a run
// over a registry that is up to date reads a page per hundred branches and a tree per
// branch and writes nothing.
func (c *Controller) Sync(ctx context.Context, logger *slog.Logger, args *Args) error {
	template, err := c.registry.Template(ctx)
	if err != nil {
		return fmt.Errorf("read the template: %w", err)
	}
	branches, err := c.branches(ctx, args)
	if err != nil {
		return err
	}
	written := 0
	for _, branch := range branches {
		if args.Limit > 0 && written >= args.Limit {
			logger.Info("stopping at the limit", "limit", args.Limit)
			return nil
		}
		wrote, err := c.sync(ctx, logger, branch, template, args.DryRun)
		if err != nil {
			// One branch failing says nothing about the others, and a branch that
			// wasn't written is one the next run writes.
			logger.Warn("failed to write the template", "branch", branch.Name, "error", err.Error())
			continue
		}
		if wrote {
			written++
		}
	}
	return nil
}

// branches is what the run works through.
func (c *Controller) branches(ctx context.Context, args *Args) ([]*g2.Branch, error) {
	if len(args.Branches) > 0 {
		out := make([]*g2.Branch, 0, len(args.Branches))
		for _, name := range args.Branches {
			out = append(out, &g2.Branch{Name: name})
		}
		return out, nil
	}
	branches, err := c.registry.PackageBranches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the package branches: %w", err)
	}
	return branches, nil
}

// sync writes what one branch is missing, and reports whether anything was written.
func (c *Controller) sync(ctx context.Context, logger *slog.Logger, branch *g2.Branch, template map[string]*gogithub.TreeEntry, dryRun bool) (bool, error) {
	held, err := c.registry.BlobSHAs(ctx, branch.Name)
	if err != nil {
		return false, err //nolint:wrapcheck // the error names the ref
	}
	// The blobs rather than the content: a git object belongs to the repository, so a
	// branch holding the template's blob holds the template's file, and a tree naming it
	// uploads nothing.
	entries := make([]*gogithub.TreeEntry, 0, len(template))
	paths := make([]string, 0, len(template))
	for path, entry := range template {
		if held[path] == entry.GetSHA() {
			continue
		}
		entries = append(entries, entry)
		paths = append(paths, path)
	}
	if len(entries) == 0 {
		return false, nil
	}
	logger.Info("writing the template", "branch", branch.Name, "files", paths)
	if dryRun {
		return true, nil
	}
	parent, err := c.parent(ctx, branch)
	if err != nil {
		return false, err
	}
	if parent == "" {
		// A branch that isn't there, which is what a run told a branch name can be
		// given.
		return false, nil
	}
	if err := c.registry.PushEntries(ctx, branch.Name, parent, message(paths), entries); err != nil {
		return false, fmt.Errorf("push the template onto the package branch: %w", err)
	}
	return true, nil
}

// parent is the commit to write onto. A run that listed the branches already knows it; one
// that was told which branch to write asks.
func (c *Controller) parent(ctx context.Context, branch *g2.Branch) (string, error) {
	if branch.SHA != "" {
		return branch.SHA, nil
	}
	sha, err := c.registry.BranchSHA(ctx, branch.Name)
	if err != nil {
		return "", err //nolint:wrapcheck // the error names the branch
	}
	return sha, nil
}

// message is what the commit says.
func message(paths []string) string {
	if len(paths) == 1 {
		return "ci: take " + paths[0] + " from the template\n\n" + why
	}
	return fmt.Sprintf("ci: take %d files from the template\n\n%s", len(paths), why)
}

// why is what a reader of one of these commits needs: the file is here because a workflow
// for a branch is read from that branch, and it holds a call because main is where it can
// be changed.
const why = `A workflow for a branch is read from that branch, so the ones a package branch
needs are on it rather than on main. They hold nothing but a call into main, which
is where they can be improved for every package at once, and the call is what
changed.`
