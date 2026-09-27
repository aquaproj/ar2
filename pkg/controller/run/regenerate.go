package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/summary"
)

var (
	// errPullRequestInFlight is returned when the package already has one open.
	errPullRequestInFlight = errors.New("the package has an open pull request, and committing would reset the branch it is on")
	// errNoDefinition is returned when the package's branch holds no definition.
	errNoDefinition = errors.New("the package's branch holds no definition to generate from")
	// errVersionNotHeld is returned for a version the registry doesn't have.
	errVersionNotHeld = errors.New("the registry holds no such version of the package")
	// errNothingWaiting is returned when nothing is waiting for a definition.
	errNothingWaiting = errors.New("the package has no pull request of versions waiting for a definition")
	// errNoBranch is returned when the branch went between finding the pull request and
	// committing to it.
	errNoBranch = errors.New("the branch isn't there")
)

// RegenerateInput is the one package a regeneration is for.
type RegenerateInput struct {
	PkgName string
	// Versions are the versions to generate again. Empty is every version the
	// registry holds for the package.
	Versions []string
	// Verify extracts every asset to check files[].src against the archive.
	Verify bool
	// Base is aqua-registry's definition of the package, which supplies what a
	// release can't express.
	Base *aquaregistry.PackageInfo
	// RegistryRef is the aqua-registry ref Base was read from.
	RegistryRef string
	// DryRun says which versions would change and commits nothing.
	DryRun bool
	// Pending regenerates what is on the package's open pull request of versions waiting
	// for a definition, from the definition that pull request carries, rather than what
	// the registry holds.
	Pending bool
}

// Regenerate generates versions the registry already holds, and opens a pull request
// with the ones that come out different.
//
// It is the repair. What the registry serves is frozen per version, so a definition
// that turns out wrong -- an all_assets_filter that let another program's asset
// through, a files[].src relocated to the wrong thing -- is wrong in every file
// generated under it, and a run won't touch them: it generates what is missing.
// Fixing the definition is only half of it, and the other half is this.
//
// It lives beside the loop because it is the same pipeline -- generate, verify,
// check the attestations, render the templates -- and it is a separate command
// because of what it can do. A run only ever adds, so a cron can be trusted with it;
// this overwrites what the registry already publishes. A flag on 'ar2 run' would put
// that within reach of whatever runs the loop unattended.
func (c *Controller) Regenerate(ctx context.Context, logger *slog.Logger, in *RegenerateInput) (int, error) {
	// A regeneration takes no turn, so there is no progress to report: what it says is
	// what it couldn't generate.
	defer c.report(logger, nil)
	logger = logger.With("package", in.PkgName)

	where, err := c.where(ctx, logger, in)
	if err != nil {
		return 0, err
	}
	def, err := c.definitionOnRef(ctx, where.ref, in.PkgName)
	if err != nil {
		return 0, err
	}
	versions, err := c.versionsToRegenerate(ctx, logger, in, where)
	if err != nil {
		return 0, err
	}

	changed, err := c.differingVersions(ctx, logger, in, def, versions, where)
	if err != nil {
		return 0, err
	}
	if len(changed) == 0 {
		// Which is the ordinary outcome of regenerating what nothing has changed
		// under, and the reason this is safe to point at a whole package.
		logger.Info("every version is what the definition already produces",
			"num_of_versions", len(versions))
		return 0, nil
	}
	if in.DryRun {
		for _, r := range changed {
			logger.Info("the version would change", "version", r.version.Version)
		}
		return len(changed), nil
	}
	if err := c.commitRegenerated(ctx, logger, in.PkgName, changed, where); err != nil {
		return 0, err
	}
	return len(changed), nil
}

// where says which branch a regeneration reads and writes.
//
// The registry's own for a version it holds: the definition on the package branch made the
// file, and the correction goes to a pull request of its own.
//
// The pull request's for a version waiting for a definition. What is being regenerated exists
// only there, the definition to generate it from is the one somebody has just written on it,
// and the result belongs on the same branch: a version and the definition that makes it true
// merge together or not at all.
func (c *Controller) where(ctx context.Context, logger *slog.Logger, in *RegenerateInput) (*target, error) {
	if !in.Pending {
		if err := c.noPullRequestInFlight(ctx, in.PkgName); err != nil {
			return nil, err
		}
		return &target{ref: g2.BranchName(in.PkgName)}, nil
	}
	pr, err := c.g2.WaitingPullRequest(ctx, in.PkgName)
	if err != nil {
		return nil, fmt.Errorf("look for the pull request of the versions waiting for a definition: %w", err)
	}
	if pr == nil {
		return nil, fmt.Errorf("%w: %s", errNothingWaiting, in.PkgName)
	}
	logger.Info("regenerating what is waiting for a definition",
		"number", pr.GetNumber(), "branch", pr.GetHead().GetRef())
	return &target{ref: pr.GetHead().GetRef(), pending: true}, nil
}

// target is the branch a regeneration works on.
type target struct {
	ref string
	// pending says the branch is a pull request's, so what is written goes onto it rather
	// than into a pull request of its own.
	pending bool
}

// noPullRequestInFlight refuses a package that already has one open.
//
// A commit is written against the package branch and the head branch is pointed at
// it, so an open pull request's commits would be discarded -- including the ones
// somebody is in the middle of reading.
func (c *Controller) noPullRequestInFlight(ctx context.Context, pkgName string) error {
	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return fmt.Errorf("list the packages with an open pull request: %w", err)
	}
	if _, ok := inFlight[g2.HeadBranchName(pkgName)]; ok {
		return fmt.Errorf("%w: %s", errPullRequestInFlight, pkgName)
	}
	return nil
}

// definitionOnBranch returns the definition the package is generated from.
//
// The branch's own, and nothing else. aqua-registry's converted definition is what a
// run writes the first time it reaches a package, and generating from it here would
// produce files the registry's own definition doesn't produce -- which is the thing
// this command exists to correct rather than to cause.
func (c *Controller) definitionOnRef(ctx context.Context, ref, pkgName string) (*definition, error) {
	cfg, err := c.g2.ConfigOnRef(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("get the package definition: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("%w: %s", errNoDefinition, pkgName)
	}
	return &definition{config: cfg, fromBranch: true}, nil
}

// versionsToRegenerate returns the versions to generate again.
//
// Only versions the registry holds. Generating one it doesn't would add it, which is
// a run's job and reaches versions in the order the registry decides rather than the
// order somebody typed.
func (c *Controller) versionsToRegenerate(ctx context.Context, logger *slog.Logger, in *RegenerateInput, where *target) ([]string, error) {
	held, err := c.g2.VersionsOnRef(ctx, logger, where.ref)
	if err != nil {
		return nil, fmt.Errorf("list the versions %s holds: %w", where.ref, err)
	}
	if len(in.Versions) == 0 {
		return allVersions(held), nil
	}
	for _, version := range in.Versions {
		if _, ok := held[version]; !ok {
			return nil, fmt.Errorf("%w: %s@%s", errVersionNotHeld, in.PkgName, version)
		}
	}
	return in.Versions, nil
}

// allVersions is every version the registry holds, in a stable order.
//
// Sorted rather than ordered by version: what this decides is only how the pull
// request reads and what order the files are generated in, and a comparison that
// knows what a version is would have to be right about every package's idea of one.
func allVersions(held map[string]struct{}) []string {
	versions := make([]string, 0, len(held))
	for version := range held {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	slices.Reverse(versions)
	return versions
}

// differingVersions generates each version and keeps the ones whose file would
// change.
//
// The comparison is on the bytes that would be committed against the bytes the
// branch holds, which is the only thing that answers whether there is anything to
// do. A regeneration that produced the same file for every version is what a package
// nothing has changed under looks like, and it should come to nothing rather than to
// a pull request.
func (c *Controller) differingVersions(ctx context.Context, logger *slog.Logger, in *RegenerateInput, def *definition, versions []string, where *target) ([]*regenerated, error) {
	input := &Input{
		Verify:      in.Verify,
		PkgInfos:    map[string]*aquaregistry.PackageInfo{in.PkgName: in.Base},
		RegistryRef: in.RegistryRef,
	}
	changed := make([]*regenerated, 0, len(versions))
	for _, tag := range versions {
		v, content, err := c.regenerate(ctx, logger, input, def, in.PkgName, tag)
		if err != nil {
			// One version failing is not a reason to leave the others wrong. It is
			// reported the way a run reports it, and the pull request carries what
			// worked.
			logger.Warn("failed to generate registry.json again",
				"version", tag, "error", err.Error())
			continue
		}
		held, err := c.g2.File(ctx, where.ref, aquag2.Path(tag))
		if err != nil {
			return nil, fmt.Errorf("get the registry.json the branch holds: %w", err)
		}
		if held == content {
			logger.Debug("the version is unchanged", "version", tag)
			continue
		}
		logger.Info("the version comes out differently now", "version", tag)
		changed = append(changed, &regenerated{version: v, content: content})
	}
	return changed, nil
}

// regenerated is one version generated again, and the file it would be committed as.
//
// The rendering is kept rather than done again at commit time, because it is what the
// comparison was made on: rendering twice would leave the file that was compared and
// the file that is committed two different things.
type regenerated struct {
	version *version
	content string
}

// regenerate generates one version and renders it the way it would be committed.
func (c *Controller) regenerate(ctx context.Context, logger *slog.Logger, input *Input, def *definition, pkgName, tag string) (*version, string, error) {
	v, err := c.generate(ctx, logger, input, def, pkgName, tag)
	if err != nil {
		c.problems = append(c.problems, &summary.Problem{
			Package: pkgName, Version: tag, Reason: err.Error(),
		})
		return nil, "", err
	}
	content, err := marshal(v.Registry)
	if err != nil {
		return nil, "", err
	}
	return v, content, nil
}

// openRegeneratedPullRequest commits the versions that changed and opens the pull
// request for them.
//
// Auto-merge is never turned on. Every other pull request the registry gets adds a
// version nothing held, so CI passing is the whole of what it needed; this one
// replaces what the registry already serves, and what CI can say about the new file
// is that it describes a real release -- not that replacing the old one was right.
func (c *Controller) commitRegenerated(ctx context.Context, logger *slog.Logger, pkgName string, versions []*regenerated, where *target) error {
	base, err := c.regenerationParent(ctx, pkgName, where)
	if err != nil {
		return err
	}
	files := make([]*g2.File, 0, len(versions))
	for _, r := range versions {
		files = append(files, &g2.File{Path: aquag2.Path(r.version.Version), Content: r.content})
	}

	title := regenerateTitle(pkgName, versions)
	branch := g2.HeadBranchName(pkgName)
	if where.pending {
		// The pull request that is waiting, so that what makes the versions true arrives
		// with them. Its own tip is the parent, because the definition somebody wrote is
		// on it and a commit built on the package branch would throw that away.
		branch = where.ref
	}
	if err := c.g2.Commit(ctx, branch, base, title, files); err != nil {
		return fmt.Errorf("commit registry.json: %w", err)
	}
	if where.pending {
		logger.Info("generated again onto the pull request that was waiting",
			"branch", branch, "num_of_versions", len(versions))
		return nil
	}
	pr, err := c.g2.CreatePullRequest(ctx, logger, pkgName, title, regenerateBody(versions))
	if err != nil {
		return err //nolint:wrapcheck
	}
	if err := c.addToSummary(pkgName, generatedVersions(versions)); err != nil {
		logger.Warn("failed to write the job summary", "error", err.Error())
	}
	logger.Info("opened a pull request", "number", pr.GetNumber(),
		"num_of_versions", len(versions))
	return nil
}

// generatedVersions is what the summary is written from.
func generatedVersions(versions []*regenerated) []*version {
	out := make([]*version, 0, len(versions))
	for _, r := range versions {
		out = append(out, r.version)
	}
	return out
}

// regenerateTitle names what the pull request replaces.
func regenerateTitle(pkgName string, versions []*regenerated) string {
	if len(versions) == 1 {
		return fmt.Sprintf("fix(%s): generate %s again", pkgName, versions[0].version.Version)
	}
	return fmt.Sprintf("fix(%s): generate %d versions again", pkgName, len(versions))
}

// regenerateBody says which versions changed and that the change is a replacement.
func regenerateBody(versions []*regenerated) string {
	var b strings.Builder
	b.WriteString("Generated again by `ar2 regenerate`, from the definition on the package's branch.\n\n")
	for _, r := range versions {
		b.WriteString("- " + r.version.Version + "\n")
	}
	b.WriteString("\nEach of these comes out differently from what the branch holds, so this replaces a " +
		"file the registry is already serving. A version whose file was unchanged isn't here.\n")
	b.WriteString("\nWhat CI checks is that the new file describes the release it says it does: the assets " +
		"download, the checksums match, the archives open and `files[].src` is inside them, and every " +
		"signature the entry claims verifies. What it can't check is whether replacing the old file was " +
		"right, so auto-merge is off and this needs reading. The old file is in the branch's history.\n")
	return b.String()
}

// regenerationParent is the commit what is regenerated is built on.
//
// The package branch for a correction of what the registry holds, and the waiting pull
// request's own tip for what is on it: the definition that makes those versions true is a
// commit there, and building on the package branch would write it away.
func (c *Controller) regenerationParent(ctx context.Context, pkgName string, where *target) (string, error) {
	if !where.pending {
		base, err := c.g2.EnsurePackageBranch(ctx, pkgName)
		if err != nil {
			return "", fmt.Errorf("get the package branch: %w", err)
		}
		return base, nil
	}
	sha, err := c.g2.BranchSHA(ctx, where.ref)
	if err != nil {
		return "", fmt.Errorf("get the branch of the pull request: %w", err)
	}
	if sha == "" {
		return "", fmt.Errorf("%w: %s", errNoBranch, where.ref)
	}
	return sha, nil
}
