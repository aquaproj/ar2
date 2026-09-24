package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/attest"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/aquaproj/ar2/pkg/summary"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// Controller runs the loop.
type Controller struct {
	gh        *gogithub.Client
	generator *generate.Generator
	g2        Registry
	graphql   GraphQL
	verifier  *verify.Verifier
	attester  *attest.Checker
	summary   *summary.Writer
	// problems are the versions this run didn't publish. A run is read through its
	// summary, so what it left out belongs there rather than only in the log.
	problems []*summary.Problem
}

// Registry is aqua-registry-g2: what it holds, and how work is added to it.
type Registry interface {
	Versions(ctx context.Context, logger *slog.Logger, pkgName string) (map[string]struct{}, error)
	PackagesInFlight(ctx context.Context) (map[string]struct{}, error)
	EnsurePackageBranch(ctx context.Context, pkgName string) (string, error)
	Version(ctx context.Context, pkgName, version string) (*aquag2.Registry, error)
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
	RegistryConfig(ctx context.Context, ref string) (*g2.RegistryConfig, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	CreatePullRequest(ctx context.Context, pkgName, title, body string) (*gogithub.PullRequest, error)
}

// GraphQL is the part of GitHub's GraphQL API a run uses.
//
// EnableAutoMerge is what makes CI the gate: a pull request merges itself once the
// checks pass and stays open when they don't. The star counts are for the packages
// aqua-registry has gained since the state was built.
type GraphQL interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
	GetStars(ctx context.Context, repos []github.Repo) (map[string]int, map[string]string, error)
	FillForbiddenStars(ctx context.Context, stars map[string]int, reasons map[string]string)
	// Versions and Tags are the sweep: they say what each package's newest
	// versions are, fifty packages to a request.
	Versions(ctx context.Context, repos []github.Repo) (map[string][]string, map[string]string, error)
	Tags(ctx context.Context, repos []github.Repo) (map[string][]string, map[string]string, error)
}

// New creates a Controller.
//
// The catalogue is not among what a run touches. A package belongs in it once its
// definition is on its branch, which is after the pull request carrying it merges
// and not before, so adding it is the reconciliation's job rather than the run's:
// 'ar2 index' lists the branches that have a definition and adds what the catalogue
// is missing. A run that added it as it went would leave an entry behind whenever a
// pull request didn't merge, describing a package nothing can install.
func New(gh *gogithub.Client, generator *generate.Generator, reg Registry, graphql GraphQL, verifier *verify.Verifier) *Controller {
	return &Controller{
		gh: gh, generator: generator, g2: reg, graphql: graphql, verifier: verifier,
		attester: attest.New(gh.Repositories), summary: summary.New(),
	}
}

// Input holds the parameters of a loop run.
type Input struct {
	// Limit bounds how many package versions are generated in one run.
	Limit int
	// OutputDir is where registry.json files are written. When it is empty the
	// result goes into a pull request instead.
	OutputDir string
	// SkipPR writes the files out without creating a branch, a commit, or a pull
	// request.
	SkipPR bool
	// Verify extracts every asset to check files[].src against the archive.
	Verify bool
	// State is the state to read the order from and record progress into.
	State *state.State
	// PkgInfos are aqua-registry's definitions, keyed by package name.
	PkgInfos map[string]*aquaregistry.PackageInfo
	// RegistryRef is the aqua-registry ref the definitions were read from. A
	// package's aqua gr configuration is read from the same ref, so that the two
	// halves of a definition come from one state of that repository.
	RegistryRef string
	// BaseBranch is aqua-registry-g2's default branch, where the registry's own
	// configuration is.
	BaseBranch string
}

// Run generates registry.json for up to Limit package versions.
//
// It returns the number generated. Nothing is recorded: the next run asks
// aqua-registry-g2 what it holds, so whatever didn't make it in is picked up again.
//
// The limit bounds versions attempted, not versions generated. Counting only what
// succeeded meant a package that fails for every version — a jar with no platform in
// its name, say — consumed no budget, so the run worked through all of its versions
// and then through every other package, spending an entire run's API calls on
// failures.
func (c *Controller) Run(ctx context.Context, logger *slog.Logger, input *Input) (int, error) {
	// Whatever the run leaves out is written where the run is read, however it ends.
	defer c.reportProblems(logger)

	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return 0, fmt.Errorf("list the packages with an open pull request: %w", err)
	}
	candidates, err := c.candidates(ctx, logger, input)
	if err != nil {
		return 0, err
	}
	// What each package's newest versions are, for the whole registry, before any of
	// it is worked on. It says which packages have anything to do at all, which is
	// most of what a run decides and used to cost a request each.
	swept := c.sweep(ctx, logger, candidates, input.PkgInfos)
	now := time.Now()

	generated, attempted := 0, 0
	for _, candidate := range candidates {
		if attempted >= input.Limit {
			return generated, nil
		}
		todo, versions := c.todo(logger, candidate, inFlight, swept, now)
		if todo == workNone {
			continue
		}
		n, tried, err := c.runPackage(ctx, logger, input, candidate, input.Limit-attempted, todo, versions)
		if err != nil && tried == 0 {
			// A package that failed before reaching any version still cost API calls
			// and still has to move the run forward, or a failure every package
			// shares walks the whole registry.
			tried = 1
		}
		attempted += tried
		if err != nil {
			if g2.ErrNoTemplate(err) {
				// Every package would fail the same way, so the run stops instead of
				// walking the whole registry to find that out.
				return generated, err
			}
			// One package must not stop the run: a repository can be deleted or
			// renamed at any time, and the remaining packages are still worth doing.
			logger.Warn("failed to process a package", "package", candidate.Name, "error", err.Error())
			continue
		}
		generated += n
	}
	return generated, nil
}

// todo says what this package needs doing, and on which versions. workNone is
// everything the run passes over without asking anything about it.
func (c *Controller) todo(logger *slog.Logger, candidate *Candidate, inFlight map[string]struct{}, swept map[string][]string, now time.Time) (work, []string) {
	if _, ok := inFlight[g2.HeadBranchName(candidate.Name)]; ok {
		// A pull request for this package is still open. Opening a second one would
		// target the same package branch and conflict on merge, and if the first is
		// open because its CI failed, a copy of it helps no one.
		logger.Debug("skipping a package with an open pull request", "package", candidate.Name)
		return workNone, nil
	}
	versions := swept[candidate.Package.RepoOwner+"/"+candidate.Package.RepoName]
	todo := decide(candidate.Package, versions, now)
	if todo == workNone {
		// The newest versions upstream are the ones the registry holds, and its
		// history was checked recently enough. Nothing is asked about it.
		logger.Debug("nothing new for the package", "package", candidate.Name)
	}
	return todo, versions
}

// candidates is the packages to work through, in the order the state gives them,
// without the ones the registry says to leave alone.
func (c *Controller) candidates(ctx context.Context, logger *slog.Logger, input *Input) ([]*Candidate, error) {
	cfg, err := c.g2.RegistryConfig(ctx, input.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("get the registry configuration: %w", err)
	}
	// Before the sweep, so that a repository nobody expects to answer isn't asked
	// about either.
	return ignore(logger, order(input.State), cfg.Ignored()), nil
}

// ignore drops the packages the registry says to leave alone.
//
// They are dropped before anything asks GitHub about them: a package is usually on
// this list because its repository isn't there any more, and the sweep would spend a
// request finding that out every half hour.
func ignore(logger *slog.Logger, candidates []*Candidate, ignored map[string]struct{}) []*Candidate {
	if len(ignored) == 0 {
		return candidates
	}
	out := make([]*Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := ignored[candidate.Name]; ok {
			logger.Debug("the registry says to leave this package alone", "package", candidate.Name)
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// reportProblems writes the versions the run didn't publish to the job summary.
//
// Failing to say so isn't worth failing the run over: the versions are still in the
// log, and the run generated whatever else it could.
func (c *Controller) reportProblems(logger *slog.Logger) {
	if c.summary == nil {
		return
	}
	if err := c.summary.Problems(c.problems); err != nil {
		slogerr.WithError(logger, err).Warn("write the summary of what wasn't published")
	}
}

// runPackage generates the missing versions of one package, newest first, up to
// budget. It returns how many were generated and how many were attempted.
func (c *Controller) runPackage(ctx context.Context, logger *slog.Logger, input *Input, candidate *Candidate, budget int, todo work, swept []string) (int, int, error) {
	pkg := candidate.Package
	if pkg.RepoOwner == "" || pkg.RepoName == "" {
		// Versions can only be listed for a GitHub repository. Packages without one
		// need another source and aren't handled yet.
		return 0, 0, nil
	}
	// The definition is read once for the package rather than once per version: it
	// is the same file for all of them, and it decides how they are generated. A
	// package whose branch has none is converted here, before anything is generated
	// from it.
	def, err := c.resolveDefinition(ctx, logger, input, candidate.Name)
	if err != nil {
		return 0, 0, err
	}

	versions, err := c.candidateVersions(ctx, logger, input, candidate, todo, swept)
	if err != nil {
		return 0, 0, err
	}
	existing, err := c.g2.Versions(ctx, logger, candidate.Name)
	if err != nil {
		return 0, 0, fmt.Errorf("list the versions aqua-registry-g2 holds: %w", err)
	}
	// What the registry holds is the only thing that says a package is done with,
	// and it is recorded after asking rather than after opening a pull request.
	record(candidate.Package, versions, existing, todo)

	budget = limitBudget(budget, existing)

	generated, attempted := c.generateVersions(ctx, logger, input, def, candidate.Name, versions, existing, budget)
	if len(generated) == 0 {
		return 0, attempted, nil
	}

	c.reviewLostSigning(ctx, logger, candidate.Name, generated, versions, existing)

	if input.SkipPR {
		return len(generated), attempted, writeAll(input.OutputDir, candidate.Name, generated)
	}
	if err := c.openPullRequest(ctx, logger, def, candidate.Name, generated); err != nil {
		return 0, attempted, err
	}
	return len(generated), attempted, nil
}

// reviewLostSigning leaves for review any version that can be verified with less
// than the one before it.
//
// The asset names and the checksums of a release an attacker published look no
// different from any other; what is missing is the signature. Nothing else in the
// generated file would say so.
func (c *Controller) reviewLostSigning(ctx context.Context, logger *slog.Logger, pkgName string, generated []*version, versions []string, existing map[string]struct{}) {
	baseline, ok := c.signingBaseline(ctx, logger, pkgName, baselineVersion(versions, existing))
	if !ok {
		for _, v := range generated {
			v.NeedsReview = true
		}
		return
	}
	checkSigning(logger, pkgName, generated, baseline)
}

// limitBudget limits how much of the run's remaining budget one package may take.
//
// A package that g2 holds fewer than breadthDepth versions of gets only enough to
// reach it, so the run moves on to the next package instead of finishing this one.
// A package already past that takes whatever is left.
func limitBudget(budget int, existing map[string]struct{}) int {
	if len(existing) >= breadthDepth {
		return budget
	}
	if remaining := breadthDepth - len(existing); remaining < budget {
		return remaining
	}
	return budget
}

// breadthDepth is how many versions a package that has none yet gets before the run
// moves on to the next package.
//
// Without it a run spends itself on the most starred package, generating every
// version it ever released while nothing else gets a single one. With it the first
// sweep leaves every package with its newest versions, which is what makes the
// registry usable: someone installing a package almost always wants a recent
// version, and a package with nothing at all can't be installed from g2 at all.
//
// Switching on what g2 already holds needs no flag and no record of which sweep this
// is. The version list is fetched either way.
//
// The extra cost is one ListReleases per package on the second sweep, since listing
// is paid per package and generating is paid per version. Over the whole registry
// that is about 6% more API calls, roughly an hour.
const breadthDepth = 5

// generateVersions generates the versions the repository is missing, newest first,
// up to budget.
func (c *Controller) generateVersions(ctx context.Context, logger *slog.Logger, input *Input, def *definition, pkgName string, versions []string, existing map[string]struct{}, budget int) ([]*version, int) {
	generated := make([]*version, 0, budget)
	attempted := 0
	for _, tag := range versions {
		if attempted >= budget {
			return generated, attempted
		}
		if _, ok := existing[tag]; ok {
			continue
		}
		attempted++
		v, err := c.generate(ctx, logger, input, def, pkgName, tag)
		if err != nil {
			// A single version failing is normal: a release can have no assets at
			// all, and a signature that can't be verified stops the version rather
			// than being dropped from it. The next run sees the version still
			// missing and tries again.
			logger.Warn("failed to generate registry.json",
				"package", pkgName, "version", tag, "error", err.Error())
			c.problems = append(c.problems, &summary.Problem{
				Package: pkgName, Version: tag, Reason: err.Error(),
			})
			continue
		}
		generated = append(generated, v)
	}
	return generated, attempted
}

// generate builds registry.json for one package version and completes it.
func (c *Controller) generate(ctx context.Context, logger *slog.Logger, input *Input, def *definition, pkgName, tag string) (*version, error) {
	reg, err := c.generator.Generate(ctx, logger, &generate.Input{
		PkgName: pkgName,
		Version: tag,
		Base:    input.PkgInfos[pkgName],
		Config:  def.config,
	})
	if err != nil {
		return nil, fmt.Errorf("generate registry.json: %w", err)
	}
	needsReview, err := c.verifier.Fill(ctx, logger, pkgName, tag, reg, input.Verify)
	if err != nil {
		return nil, fmt.Errorf("complete registry.json: %w", err)
	}
	// After Fill, because an attestation is held against the artifact's digest and
	// Fill is what makes sure every asset has one.
	dropped, err := c.attester.Check(ctx, logger, pkgName, reg)
	if err != nil {
		return nil, fmt.Errorf("check the attestations: %w", err)
	}
	// Whatever the definition wrote as a template is filled in here: the file
	// describes one version of one environment and shouldn't leave anything to be
	// worked out at install time.
	for _, asset := range reg.Assets {
		sign.Render(asset, tag)
	}
	return &version{
		Version:     tag,
		Registry:    reg,
		NeedsReview: needsReview || dropped,
	}, nil
}

// writeAll writes the generated files out instead of opening a pull request.
func writeAll(dir, pkgName string, versions []*version) error {
	for _, v := range versions {
		if err := write(dir, pkgName, v.Version, v.Registry); err != nil {
			return err
		}
	}
	return nil
}

// write stores registry.json at the path it has on the package's branch.
func write(dir, pkgName, version string, reg *generate.Registry) error {
	// The layout mirrors aqua-registry-g2: the package branch holds
	// versions/<version>/registry-1.json, and the package name is a directory here so
	// that one run's output holds more than one package.
	path := filepath.Join(dir, filepath.Join(strings.Split(pkgName, "/")...), filepath.FromSlash(aquag2.Path(version)))
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create a directory for registry.json: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create registry.json: %w", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(reg); err != nil {
		return fmt.Errorf("write registry.json: %w", err)
	}
	return nil
}

const dirPerm = 0o750
