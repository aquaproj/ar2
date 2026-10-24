// Package versions brings the packages' lists of versions up to date with what the registry
// holds.
package versions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v93/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// Registry is the part of aqua-registry-g2 an update of the lists reads and writes.
type Registry interface {
	VersionsListFile(ctx context.Context, logger *slog.Logger, ref, id string) (*g2.File, error)
	File(ctx context.Context, ref, path string) (string, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	VersionsPullRequest(ctx context.Context) (*gogithub.PullRequest, error)
	CreateVersionsPullRequest(ctx context.Context, logger *slog.Logger, base, title, body string) (*gogithub.PullRequest, error)
}

// AutoMerger turns on auto-merge for a pull request.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// Controller updates the lists of versions.
type Controller struct {
	g2         Registry
	automerge  AutoMerger
	baseBranch string
	dryRun     bool
}

// DryRun makes Update say which lists are behind and commit nothing.
func (c *Controller) DryRun() {
	c.dryRun = true
}

// New creates a Controller. automerge may be nil, and then the pull request waits for
// somebody.
func New(registry Registry, automerge AutoMerger, baseBranch string) *Controller {
	return &Controller{g2: registry, automerge: automerge, baseBranch: baseBranch}
}

var errNotID = errors.New("not a package id")

// Update makes the versions.json of each package whose id is given say what its versions
// directory on the base branch holds, in one pull request.
//
// A pull request adding a version leaves the package's list alone, because two of them open
// for one package would conflict on it. So the list falls behind when one merges, and this
// is what brings it up: it is run for the packages a merge touched.
//
// Each list is made from the base branch, and committed onto the open pull request when
// there is one, which only ever changes lists: what it holds of an older base is replaced by
// what the base holds now, and nothing else the base gained since is touched.
func (c *Controller) Update(ctx context.Context, logger *slog.Logger, ids []string) error {
	if err := checkIDs(ids); err != nil {
		return err
	}
	pr, err := c.g2.VersionsPullRequest(ctx)
	if err != nil {
		return fmt.Errorf("look for an open pull request: %w", err)
	}
	ref := c.baseBranch
	if pr != nil {
		ref = g2.VersionsBranch
	}
	files, err := c.files(ctx, logger, ref, ids)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		logger.Info("every list of versions is up to date", "num_of_packages", len(ids))
		return nil
	}
	if c.dryRun {
		return nil
	}
	parent, err := c.g2.BranchSHA(ctx, ref)
	if err != nil {
		return fmt.Errorf("get the branch to commit onto: %w", err)
	}
	if err := c.g2.Commit(ctx, g2.VersionsBranch, parent, message(len(files)), files); err != nil {
		return fmt.Errorf("commit the lists of versions: %w", err)
	}
	if pr != nil {
		logger.Info("added to the open pull request", "number", pr.GetNumber(), "num_of_lists", len(files))
		return nil
	}
	return c.openPullRequest(ctx, logger, len(files))
}

// checkIDs refuses anything that isn't a package id, which a package name would be.
func checkIDs(ids []string) error {
	for _, id := range ids {
		if !g2.IsID(id) {
			return fmt.Errorf("%w: %s", errNotID, id)
		}
	}
	return nil
}

// files are the lists that have to change: made from the base branch, and not already what
// ref holds.
func (c *Controller) files(ctx context.Context, logger *slog.Logger, ref string, ids []string) ([]*g2.File, error) {
	out := make([]*g2.File, 0, len(ids))
	for _, id := range ids {
		file, err := c.g2.VersionsListFile(ctx, logger, c.baseBranch, id)
		if err != nil {
			return nil, fmt.Errorf("make the list of versions of %s: %w", id, err)
		}
		if file == nil {
			logger.Debug("the list of versions is up to date", "id", id)
			continue
		}
		if ref != c.baseBranch {
			held, err := c.g2.File(ctx, ref, file.Path)
			if err != nil {
				return nil, err //nolint:wrapcheck
			}
			if !file.Deleted && held == file.Content {
				logger.Debug("the open pull request already holds the list", "id", id)
				continue
			}
		}
		logger.Info("the list of versions is behind", "id", id)
		out = append(out, file)
	}
	return out, nil
}

// openPullRequest opens the pull request and has it merge itself.
//
// A list is made from the files the registry already serves, which were checked when they
// arrived, so there is nothing for a person to decide: the checks on the base branch say
// whether it is a file aqua can read, and it merges when they pass.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, n int) error {
	pr, err := c.g2.CreateVersionsPullRequest(ctx, logger, c.baseBranch, message(n), body)
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened a pull request", "number", pr.GetNumber(), "num_of_lists", n)
	if c.automerge == nil {
		return nil
	}
	if err := c.automerge.EnableAutoMerge(ctx, pr.GetNodeID()); err != nil {
		// The pull request is open and correct; it just waits for someone.
		slogerr.WithError(logger, err).Warn("failed to turn on auto-merge", "number", pr.GetNumber())
	}
	return nil
}

// message is the commit message and the pull request's title.
func message(n int) string {
	if n == 1 {
		return "chore: bring a list of versions up to date"
	}
	return fmt.Sprintf("chore: bring %d lists of versions up to date", n)
}

// body is what the pull request says.
const body = `Generated by ` + "`ar2 update-versions`" + `.

A pull request adding a version leaves the package's versions.json alone, because two of them
open for one package would conflict on it. These lists are made again from the versions each
package's directory holds on the default branch, and say nothing the directories don't.
`
