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
	// LostEnvironments names the environments the version before it covered and this
	// one doesn't.
	LostEnvironments []string
	// Unresolved names the environments whose entry points at a file the archive doesn't
	// hold. Such a version goes to a pull request of its own: what it says can't be true,
	// so it can't merge, and in the pull request with the rest it would stop the versions
	// that were right from merging either.
	Unresolved []string
}

// openPullRequest commits every version generated for a package and opens one pull
// request for them.
//
// One pull request per package rather than per version: two into the same package
// branch would conflict, since the second is written against a base the first has
// moved. It also keeps the number of pull requests to something a maintainer can
// look at.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, def *definition, pkgName string, versions []*version) error {
	base, err := c.g2.EnsurePackageBranch(ctx, pkgName)
	if err != nil {
		return fmt.Errorf("ensure the package branch: %w", err)
	}

	contents, err := c.filesToCommit(logger, def, pkgName, versions)
	if err != nil {
		return err
	}
	needsReview := contents.needsReview

	title := prTitle(pkgName, versions)
	if err := c.g2.Commit(ctx, g2.HeadBranchName(pkgName), base, title, contents.files); err != nil {
		return fmt.Errorf("commit registry.json: %w", err)
	}

	pr, err := c.g2.CreatePullRequest(ctx, logger, pkgName, title, prBody(versions, contents.unconverted, needsReview))
	if err != nil {
		return err //nolint:wrapcheck
	}
	if err := c.addToSummary(pkgName, versions); err != nil {
		logger.Warn("failed to write the job summary", "error", err.Error())
	}
	logger.Info("opened a pull request",
		"package", pkgName, "number", pr.GetNumber(), "needs_review", needsReview)

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
	// unconverted names the version_constraints of the package's definition that
	// couldn't be turned into boundaries.
	unconverted []string
}

// filesToCommit gathers everything the pull request carries, and reports whether any
// of it has to be looked at before merging.
func (c *Controller) filesToCommit(logger *slog.Logger, def *definition, pkgName string, versions []*version) (*contents, error) {
	out := &contents{files: make([]*g2.File, 0, len(versions)+1)}

	// A package whose definition isn't on its branch yet is one aqua-registry-g2
	// hasn't taken over. Converting it here means the move happens as a package is
	// worked on rather than as a migration of its own, and it arrives for review
	// beside the files generated from it.
	cfg, err := c.packageConfig(logger, def, pkgName, versions)
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		out.files = append(out.files, cfg.file)
		out.config = cfg.config
		out.needsReview = out.needsReview || cfg.needsReview
		out.unconverted = cfg.unconverted
	}

	for _, v := range versions {
		content, err := marshal(v.Registry)
		if err != nil {
			return nil, err
		}
		out.files = append(out.files, &g2.File{
			Path:    aquag2.Path(v.Version),
			Content: content,
		})
		out.needsReview = out.needsReview || v.NeedsReview
	}
	return out, nil
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

// lostEnvironmentLines describes which environments each version stopped covering.
func lostEnvironmentLines(versions []*version) []string {
	var lines []string
	for _, v := range versions {
		for _, lost := range v.LostEnvironments {
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

func prBody(versions []*version, unconverted []string, needsReview bool) string {
	var b strings.Builder
	b.WriteString("Generated by ar2.\n\n")
	for _, v := range versions {
		fmt.Fprintf(&b, "- %s", v.Version)
		if v.NeedsReview {
			b.WriteString(" (needs review)")
		}
		b.WriteString("\n")
	}
	writeLost(&b, lostLines(versions),
		"These versions can be verified with less than the version before them:",
		"A release that stops carrying its signatures is what an attacker publishing one would "+
			"look like from here, so this has to be looked at before it merges.")
	writeLost(&b, lostEnvironmentLines(versions),
		"These versions cover fewer environments than the version before them:",
		"The asset names are read from the release rather than from a template, so an asset renamed "+
			"to a spelling the parser doesn't know produces no entry for that environment at all. The "+
			"definition is where the answer goes, as a replacement naming the spelling. A release that "+
			"stopped building for the environment is the other reason, and then there is nothing to fix.")
	writeUnconverted(&b, unconverted)
	writeWaiting(&b, unconverted, needsReview)
	return b.String()
}

// writeLost says which versions lost something, and what losing it means. Nothing is
// written when nothing was lost.
func writeLost(b *strings.Builder, lost []string, heading, meaning string) {
	if len(lost) == 0 {
		return
	}
	b.WriteString("\n" + heading + "\n\n")
	for _, line := range lost {
		b.WriteString("- " + line + "\n")
	}
	b.WriteString("\n" + meaning + "\n")
}

// writeUnconverted says which version_constraints were carried over as they were.
func writeUnconverted(b *strings.Builder, unconverted []string) {
	if len(unconverted) == 0 {
		return
	}
	b.WriteString("\nThese version_constraints of the definition couldn't be turned into boundaries, " +
		"so the overrides are carried over in the order aqua-registry had them:\n\n")
	for _, constraint := range unconverted {
		b.WriteString("- `" + constraint + "`\n")
	}
	b.WriteString("\nThe conversion reverses the overrides and gives each one the lower bound of the " +
		"range it covers, which it can't do for a constraint that names versions rather than a range. " +
		"Keeping the original order resolves the same way; it is here to be read rather than because " +
		"anything is wrong.\n")
}

// writeWaiting says that auto-merge is off, and where to find out why when the body
// doesn't already say.
func writeWaiting(b *strings.Builder, unconverted []string, needsReview bool) {
	if !needsReview {
		return
	}
	b.WriteString("\nAuto-merge is off. What it is waiting on is above")
	if len(unconverted) == 0 {
		b.WriteString(", or in the run's log: `files[].src` may not have matched the archive and been " +
			"relocated by name, which is a guess, or the archive may not have been checkable at all")
	}
	b.WriteString(".\n")
}

// openUnresolved opens the pull request of the versions that can't merge as they stand.
//
// One pull request for all of them, on a branch named after the oldest: what they are waiting
// for is one thing said once in the definition -- the layout of an era of the release -- so
// fixing it fixes them together, and the work belongs in one place.
//
// It carries them as generated, wrong in the way the definition is wrong: the entry names a
// file the archive doesn't hold anywhere inside it. Left in the pull request with the rest of
// the run's versions they would keep those from merging; left out of the registry entirely
// they would be a line in a summary nobody reads.
//
// So they are a pull request, which is a place to work: the definition they need goes on the
// same branch, and they are generated again from there. Auto-merge is never turned on -- what
// they say can't be true yet.
func (c *Controller) openUnresolved(ctx context.Context, logger *slog.Logger, pkgName string, versions []*version, inFlight map[string]struct{}) error {
	if len(versions) == 0 {
		return nil
	}
	if branch, ok := waiting(pkgName, inFlight); ok {
		// Committing would reset that branch, and what is on it may be the definition
		// somebody is in the middle of writing.
		logger.Info("the package already has versions waiting for a definition",
			"package", pkgName, "branch", branch)
		return nil
	}

	// The versions come newest first, so the oldest is the last of them. Named after it, a
	// later run that finds the same era finds the same branch.
	oldest := versions[len(versions)-1]
	branch := g2.VersionHeadBranchName(pkgName, oldest.Version)

	base, err := c.g2.EnsurePackageBranch(ctx, pkgName)
	if err != nil {
		return fmt.Errorf("ensure the package branch: %w", err)
	}
	files := make([]*g2.File, 0, len(versions))
	for _, v := range versions {
		content, err := marshal(v.Registry)
		if err != nil {
			return err
		}
		files = append(files, &g2.File{Path: aquag2.Path(v.Version), Content: content})
	}

	title := unresolvedTitle(pkgName, versions)
	if err := c.g2.Commit(ctx, branch, base, title, files); err != nil {
		return fmt.Errorf("commit registry.json: %w", err)
	}
	pr, err := c.g2.CreatePullRequestFrom(ctx, logger, branch, g2.BranchName(pkgName), title,
		unresolvedBody(versions))
	if err != nil {
		return err //nolint:wrapcheck
	}
	c.g2.Label(ctx, logger, pr.GetNumber(), g2.NeedsDefinitionLabel)
	logger.Info("opened a pull request for the versions that need a definition",
		"package", pkgName, "num_of_versions", len(versions), "number", pr.GetNumber())
	return nil
}

// waiting says the package already has versions waiting for a definition, and on which
// branch.
//
// Any branch of the package's but its own: the one a run opens for every version it generated
// is the package's own, and these are named after a version.
func waiting(pkgName string, inFlight map[string]struct{}) (string, bool) {
	for branch := range inFlight {
		if g2.IsVersionHeadBranch(pkgName, branch) {
			return branch, true
		}
	}
	return "", false
}

func unresolvedTitle(pkgName string, versions []*version) string {
	if len(versions) == 1 {
		return fmt.Sprintf("feat(%s): add %s, which needs the definition to say more",
			pkgName, versions[0].Version)
	}
	return fmt.Sprintf("feat(%s): add %d versions that need the definition to say more",
		pkgName, len(versions))
}

// unresolvedBody says what the versions are waiting for.
func unresolvedBody(versions []*version) string {
	var b strings.Builder
	b.WriteString("Generated by ar2, and waiting for the definition to say more.\n\n")
	b.WriteString("The archive holds no file of the name `files[].src` gives, anywhere inside it:\n\n")
	for _, v := range versions {
		for _, env := range v.Unresolved {
			b.WriteString("- " + v.Version + " " + env + "\n")
		}
	}
	b.WriteString("\nWhat that usually means is that the release changed its layout and the definition " +
		"describes one era of it. The versions of the other era need the definition to say so, which " +
		"nothing but a person can write.\n")
	b.WriteString("\nThey are here together, and without the versions the same run generated, so that " +
		"those could merge: one definition says what this whole era needs, and a version that can't " +
		"merge shouldn't stop one that can.\n")
	b.WriteString("\nWhat to do is to put the definition they need on this branch -- a " +
		"`version_overrides` entry with its own `files` -- and generate them again from there, which " +
		"is what makes the files below true.\n")
	b.WriteString("\nAuto-merge is off, and the checks fail until the files describe the release. " +
		"Closing this says the versions aren't worth a definition.\n")
	return b.String()
}
