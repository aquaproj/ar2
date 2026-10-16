package run

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/aquaproj/ar2/pkg/g2"
)

// openReview puts the versions left for review in the package's one pull request of them.
//
// A version is left for review mostly for a reason its package's other versions share:
// anomalyco/opencode started publishing a baseline build and its desktop app beside the CLI,
// nothing told them apart on amd64, and twenty versions became twenty pull requests to read
// and then to close once the definition was fixed. Gathered in one, they are one place to
// look and one to close. The versions that need nothing don't wait for them: each is still a
// pull request of its own, merging itself.
//
// A package the registry doesn't hold yet isn't gathered. Its first version's pull request
// carries the definition, and no other version is generated until that merges.
//
// When the pull request is open already, the versions are committed on top of its branch. It
// never merges itself.
func (c *Controller) openReview(ctx context.Context, logger *slog.Logger, def *definition, pkgName string, versions []*version, inFlight map[string]struct{}) error {
	if len(versions) == 0 {
		return nil
	}
	branch, ok := c.g2.ReviewHeadBranch(pkgName)
	if !ok {
		return fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}
	contents, err := c.filesToCommit(logger, def, pkgName, versions)
	if err != nil {
		return err
	}
	title := reviewTitle(pkgName, versions)
	if _, open := inFlight[branch]; open {
		parent, err := c.g2.BranchSHA(ctx, branch)
		if err != nil {
			return fmt.Errorf("read the branch of the versions left for review: %w", err)
		}
		if err := c.g2.CommitVersions(ctx, pkgName, branch, parent, title, contents.files); err != nil {
			return fmt.Errorf("commit registry.json: %w", err)
		}
		logger.Info("added versions to the pull request of the versions left for review",
			"package", pkgName, "num_of_versions", len(versions), "branch", branch)
		return c.summarize(logger, pkgName, versions)
	}

	base, err := c.g2.PackageBase(ctx, pkgName)
	if err != nil {
		return fmt.Errorf("take the package over: %w", err)
	}
	if err := c.g2.CommitVersions(ctx, pkgName, branch, base, title, contents.files); err != nil {
		return fmt.Errorf("commit registry.json: %w", err)
	}
	pr, err := c.g2.CreatePullRequestFrom(ctx, logger, branch, g2.DefaultBranch, title,
		prBody(versions, contents.unconverted, true, releasesOf(def.config)))
	if err != nil {
		return err //nolint:wrapcheck
	}
	inFlight[branch] = struct{}{}
	logger.Info("opened a pull request of the versions left for review",
		"package", pkgName, "num_of_versions", len(versions), "number", pr.GetNumber())
	return c.summarize(logger, pkgName, versions)
}

// summarize writes the versions into the job summary, which failing to do fails nothing.
func (c *Controller) summarize(logger *slog.Logger, pkgName string, versions []*version) error {
	if err := c.addToSummary(pkgName, versions); err != nil {
		logger.Warn("failed to write the job summary", "error", err.Error())
	}
	return nil
}

// known is the versions the registry holds, and those plus the ones in the package's open
// pull request of versions left for review. The second are a person's to decide on, so a run
// passes them over as it does the ones the registry holds.
func (c *Controller) known(ctx context.Context, logger *slog.Logger, pkgName string, inFlight map[string]struct{}) (map[string]struct{}, map[string]struct{}, error) {
	existing, err := c.g2.Versions(ctx, logger, pkgName)
	if err != nil {
		return nil, nil, fmt.Errorf("list the versions aqua-registry-g2 holds: %w", err)
	}
	reviewing, err := c.inReview(ctx, logger, pkgName, inFlight)
	if err != nil {
		return nil, nil, err
	}
	return existing, union(existing, reviewing), nil
}

// inReview is the versions in the package's open pull request of those left for review.
func (c *Controller) inReview(ctx context.Context, logger *slog.Logger, pkgName string, inFlight map[string]struct{}) (map[string]struct{}, error) {
	branch, ok := c.g2.ReviewHeadBranch(pkgName)
	if !ok {
		return map[string]struct{}{}, nil
	}
	if _, open := inFlight[branch]; !open {
		return map[string]struct{}{}, nil
	}
	versions, err := c.g2.VersionsOnRef(ctx, logger, branch, pkgName)
	if err != nil {
		return nil, fmt.Errorf("list the versions left for review: %w", err)
	}
	return versions, nil
}

// union is the versions in either set.
func union(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

func reviewTitle(pkgName string, versions []*version) string {
	if len(versions) == 1 {
		return fmt.Sprintf("feat(%s): add %s, which needs review", pkgName, versions[0].Version)
	}
	return fmt.Sprintf("feat(%s): add %d versions that need review", pkgName, len(versions))
}
