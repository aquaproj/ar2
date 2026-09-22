package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
)

// version is one generated registry.json waiting to be committed.
type version struct {
	Version     string
	Registry    *generate.Registry
	NeedsReview bool
	// LostSigning names what this version can no longer be verified with that the
	// version before it could, as "<environment>: <kind>".
	LostSigning []string
}

// openPullRequest commits every version generated for a package and opens one pull
// request for them.
//
// One pull request per package rather than per version: two into the same package
// branch would conflict, since the second is written against a base the first has
// moved. It also keeps the number of pull requests to something a maintainer can
// look at.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, input *Input, config *aquag2.Config, pkgName string, versions []*version) error {
	base, err := c.g2.EnsurePackageBranch(ctx, pkgName)
	if err != nil {
		return fmt.Errorf("ensure the package branch: %w", err)
	}

	contents, err := c.filesToCommit(ctx, logger, input, config, pkgName, versions)
	if err != nil {
		return err
	}
	needsReview := contents.needsReview

	title := prTitle(pkgName, versions)
	if err := c.g2.Commit(ctx, g2.HeadBranchName(pkgName), base, title, contents.files); err != nil {
		return fmt.Errorf("commit registry.json: %w", err)
	}

	pr, err := c.g2.CreatePullRequest(ctx, pkgName, title, prBody(versions, needsReview))
	if err != nil {
		return err //nolint:wrapcheck
	}
	if err := c.addToSummary(pkgName, versions); err != nil {
		logger.Warn("failed to write the job summary", "error", err.Error())
	}
	logger.Info("opened a pull request",
		"package", pkgName, "number", pr.GetNumber(), "needs_review", needsReview)

	c.addToIndex(ctx, logger, pkgName, contents.config)

	if needsReview {
		// A relocated files[].src is a guess. Merging it without anyone looking is
		// exactly what the archive check exists to prevent.
		logger.Warn("leaving the pull request for review", "number", pr.GetNumber())
		return nil
	}
	if err := c.graphql.EnableAutoMerge(ctx, pr.GetNodeID()); err != nil {
		// The pull request is the work; auto-merge is how it lands without anyone
		// clicking. Failing the package here would leave the pull request open and
		// uncounted, so the next package would get one too and the run would never
		// reach its limit. It is reported and the pull request waits for a human.
		logger.Warn("failed to enable auto-merge; the pull request stays open",
			"number", pr.GetNumber(), "error", err.Error())
	}
	return nil
}

// contents is everything a pull request carries.
type contents struct {
	files []*g2.File
	// config is the definition this pull request brings, and nil when the branch
	// already had one. It is what the catalogue entry is made of.
	config      *aquag2.Config
	needsReview bool
}

// filesToCommit gathers everything the pull request carries, and reports whether any
// of it has to be looked at before merging.
func (c *Controller) filesToCommit(ctx context.Context, logger *slog.Logger, input *Input, config *aquag2.Config, pkgName string, versions []*version) (*contents, error) {
	out := &contents{files: make([]*g2.File, 0, len(versions)+1)}

	// A package whose definition isn't on its branch yet is one aqua-registry-g2
	// hasn't taken over. Converting it here means the move happens as a package is
	// worked on rather than as a migration of its own, and it arrives for review
	// beside the files generated from it.
	cfg, err := c.packageConfig(ctx, logger, input, config, pkgName, versions)
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		out.files = append(out.files, cfg.file)
		out.config = cfg.config
		out.needsReview = out.needsReview || cfg.needsReview
	}

	for _, v := range versions {
		content, err := marshal(v.Registry)
		if err != nil {
			return nil, err
		}
		out.files = append(out.files, &g2.File{
			Path:    fmt.Sprintf("%s/%s/registry.json", g2.VersionDir, v.Version),
			Content: content,
		})
		out.needsReview = out.needsReview || v.NeedsReview
	}
	return out, nil
}

// addToIndex puts a package the run has just taken over into the catalogue.
//
// Only a package arriving with its definition: any other has been in the catalogue
// since the run that brought it. The definition is the one just written, because the
// branch won't hold it until this pull request merges.
//
// A failure here doesn't fail the package. The catalogue update is a separate pull
// request against a separate branch, and the reconciliation that runs on a schedule
// adds whatever was missed once the definition has landed.
func (c *Controller) addToIndex(ctx context.Context, logger *slog.Logger, pkgName string, config *aquag2.Config) {
	if config == nil || c.index == nil {
		return
	}
	if err := c.index.AddPackage(ctx, logger, pkgName, config); err != nil {
		logger.Warn("failed to add the package to the catalogue",
			"package", pkgName, "error", err.Error())
	}
}

// marshal renders registry.json the way it is stored: on one line.
//
// Nothing reads it by eye. Indenting it is 30% of the bytes every aqua user
// downloads and costs nothing in the repository, where git compresses the
// whitespace away — measured at 6,094 against 4,283 bytes raw and no difference
// packed. The indented form goes to the job summary instead.
func marshal(reg *generate.Registry) (string, error) {
	b, err := json.Marshal(reg)
	if err != nil {
		return "", fmt.Errorf("marshal registry.json: %w", err)
	}
	return string(b) + "\n", nil
}

// addToSummary records what was generated, indented, where a person can read it.
func (c *Controller) addToSummary(pkgName string, versions []*version) error {
	if c.summary == nil {
		return nil
	}
	m := make(map[string]any, len(versions))
	for _, v := range versions {
		m[v.Version] = v.Registry
	}
	return c.summary.Add(pkgName, m) //nolint:wrapcheck
}

// lostLines describes what each version stopped being verifiable with.
func lostLines(versions []*version) []string {
	var lines []string
	for _, v := range versions {
		for _, lost := range v.LostSigning {
			lines = append(lines, v.Version+" "+lost)
		}
	}
	return lines
}

func prTitle(pkgName string, versions []*version) string {
	if len(versions) == 1 {
		return fmt.Sprintf("feat(%s): add %s", pkgName, versions[0].Version)
	}
	return fmt.Sprintf("feat(%s): add %d versions", pkgName, len(versions))
}

func prBody(versions []*version, needsReview bool) string {
	var b strings.Builder
	b.WriteString("Generated by ar2.\n\n")
	for _, v := range versions {
		fmt.Fprintf(&b, "- %s", v.Version)
		if v.NeedsReview {
			b.WriteString(" (needs review)")
		}
		b.WriteString("\n")
	}
	if lost := lostLines(versions); len(lost) > 0 {
		b.WriteString("\nThese versions can be verified with less than the version before them:\n\n")
		for _, line := range lost {
			b.WriteString("- " + line + "\n")
		}
		b.WriteString("\nA release that stops carrying its signatures is what an attacker publishing one " +
			"would look like from here, so this has to be looked at before it merges.\n")
	}
	if needsReview {
		b.WriteString("\nAuto-merge is off. Either a version lost the signing the one before it had, " +
			"or `files[].src` didn't match the archive and was relocated by name, which is a guess, " +
			"or the archive couldn't be checked at all. See the run's log.\n")
	}
	return b.String()
}
