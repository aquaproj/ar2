package run

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/attest"
	"github.com/aquaproj/ar2/pkg/forge"
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
	gh *gogithub.Client
	// httpClient reads a forge instance, which is not GitHub and has no client of
	// its own here: one is made per package, for the host its definition names.
	httpClient *http.Client
	generator  *generate.Generator
	g2         Registry
	graphql    GraphQL
	// autoMerge turns auto-merge on, where it is the app's token that must. Nil leaves
	// it to the reading client. See UseAutoMerger.
	autoMerge AutoMerger
	// issues is where an environment the registry can't offer is said. Nil says it in
	// the log and the summary and nowhere else. See UseIssues.
	issues      Issues
	issuesOwner string
	issuesRepo  string
	verifier    *verify.Verifier
	attester    *attest.Checker
	summary     *summary.Writer
	// problems are the versions this run didn't publish. A run is read through its
	// summary, so what it left out belongs there rather than only in the log.
	problems []*summary.Problem
	// renamed is the repositories the sweep found aren't where the registry says.
	renamed []*summary.Rename
	// repoIDs is each repository's numeric id, as the sweep found it, keyed by the
	// owner/name the registry has for it.
	repoIDs map[string]int64
	// renamer moves a package to the name its repository has now. Nil reports the
	// rename and leaves it, which is what a run that can't create a branch does.
	renamer Renamer
}

// Registry is aqua-registry-g2: what it holds, and how work is added to it.
//
// The two halves are separate because they are read differently. What the registry holds
// decides what a run does; the pull requests are how what it did reaches a person.
type Registry interface {
	Contents
	PullRequests
}

// Contents is what aqua-registry-g2 holds, and how a head branch of it is written.
type Contents interface {
	Definitions
	Branches
	Versions(ctx context.Context, logger *slog.Logger, pkgName string) (map[string]struct{}, error)
	// VersionsOnRef reads a branch other than the default one, which is where a version
	// waiting for a definition lives.
	VersionsOnRef(ctx context.Context, logger *slog.Logger, ref, pkgName string) (map[string]struct{}, error)
	Version(ctx context.Context, pkgName, version string) (*aquag2.Registry, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	PackageBase(ctx context.Context, pkgName string) (string, error)
	// File is the bytes a ref holds at a path, or "" when it holds nothing there.
	// A regeneration compares what it would commit against them.
	File(ctx context.Context, ref, path string) (string, error)
	CommitPackage(ctx context.Context, logger *slog.Logger, pkgName, branch, parent, message string, files []*g2.File) error
	// CommitWaiting is the commit of versions waiting for a definition, which leaves the
	// list of versions alone.
	CommitWaiting(ctx context.Context, pkgName, branch, parent, message string, files []*g2.File) error
}

// Definitions is what the registry says a package is, and what it says about itself.
//
// ConfigOnRef reads a branch other than the default one, which is where a definition
// somebody has just written lives until its pull request merges.
type Definitions interface {
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
	ConfigOnRef(ctx context.Context, ref, pkgName string) (*aquag2.Config, error)
	RegistryConfig(ctx context.Context, ref string) (*g2.RegistryConfig, error)
}

// Branches is where a package's work goes.
//
// A head branch is named after the package's id, so each of these answers false for a
// package the registry doesn't hold yet -- which is the package PackageBase is about to mint
// an id for.
type Branches interface {
	// Dir is the directory holding the package, which the paths of what it holds are
	// under.
	Dir(pkgName string) (string, bool)
	HeadBranch(pkgName string) (string, bool)
	VersionHeadBranch(pkgName, version string) (string, bool)
	IsVersionHeadBranch(pkgName, branch string) bool
}

// PullRequests is how a run puts what it generated to a person, and how it sees what is
// already waiting for one.
type PullRequests interface {
	PackagesInFlight(ctx context.Context) (map[string]struct{}, error)
	// WaitingPullRequest is the package's open pull request of versions waiting for a
	// definition, which is what a regeneration of something unmerged works on.
	WaitingPullRequest(ctx context.Context, pkgName string) (*gogithub.PullRequest, error)
	CreatePullRequest(ctx context.Context, logger *slog.Logger, pkgName, title, body string) (*gogithub.PullRequest, error)
	// CreatePullRequestFrom opens one from a branch of its own, which is what the versions
	// waiting for a definition are on.
	CreatePullRequestFrom(ctx context.Context, logger *slog.Logger, head, base, title, body string) (*gogithub.PullRequest, error)
	// Label is how the pull requests waiting for a definition are found together.
	Label(ctx context.Context, logger *slog.Logger, number int, name string)
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
	Versions(ctx context.Context, repos []github.Repo) (*github.Sweep, error)
	Tags(ctx context.Context, repos []github.Repo) (*github.Sweep, error)
}

// New creates a Controller.
//
// The catalogue is not among what a run touches. A package belongs in it once the
// default branch holds its definition, which is after the pull request carrying it
// merges and not before, so adding it is the reconciliation's job rather than the run's:
// 'ar2 index' reads every definition and adds what the catalogue is missing. A run that added it as it went would leave an entry behind whenever a
// pull request didn't merge, describing a package nothing can install.
func New(gh *gogithub.Client, httpClient *http.Client, generator *generate.Generator, reg Registry, graphql GraphQL, verifier *verify.Verifier, renamer Renamer) *Controller {
	return &Controller{
		gh: gh, httpClient: httpClient, generator: generator, g2: reg, graphql: graphql,
		verifier: verifier, renamer: renamer, attester: attest.New(gh.Repositories),
		summary: summary.New(),
	}
}

// AutoMerger turns a pull request over to its checks.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// UseAutoMerger says what turns auto-merge on, where the reading client does it otherwise.
//
// Which token does it decides what happens after the checks pass. The merge is a push onto
// the default branch, and GitHub raises no workflow run for a push GITHUB_TOKEN made.
// Turned on with the app that opened the pull request, the merge is that app's and what
// waits on the push hears about it.
//
// Reading stays with the repository's own token: the sweep is the heavy part of a run, and
// its rate limit is the repository's rather than an installation's.
func (c *Controller) UseAutoMerger(m AutoMerger) {
	c.autoMerge = m
}

// Renamer moves a package to the name its repository has now.
type Renamer interface {
	Rename(ctx context.Context, logger *slog.Logger, from, to string) error
}

// Input holds the parameters of a loop run.
type Input struct {
	// Packages are the packages to work through instead of the order. Empty is the
	// order itself, which is what a scheduled run does.
	//
	// Nothing else changes: a named package is taken over, generated and put to a pull
	// request the way the order would have, whenever its turn came.
	Packages []string
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
	defer c.report(logger, input.State)

	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return 0, fmt.Errorf("list the packages with an open pull request: %w", err)
	}
	cfg, err := c.g2.RegistryConfig(ctx, input.BaseBranch)
	if err != nil {
		return 0, fmt.Errorf("get the registry configuration: %w", err)
	}
	candidates := c.candidates(logger, input, cfg)
	// What each package's newest versions are, for the whole registry, before any of
	// it is worked on. It says which packages have anything to do at all, which is
	// most of what a run decides and used to cost a request each.
	swept, renamed := c.sweep(ctx, logger, candidates, input.PkgInfos)
	// Before anything is generated: a package whose branch has just been carried over
	// to another name would otherwise be generated under the name nothing holds.
	moved := c.moveRenamed(ctx, logger, input, renamed)
	now := time.Now()

	generated, attempted := 0, 0
	for _, candidate := range candidates {
		if attempted >= input.Limit {
			return generated, nil
		}
		// The run reached this package, so it goes behind the ones it hasn't. Not
		// whether there was anything to do, and not whether it worked: a package
		// that fails every time would otherwise take the same share of every run.
		candidate.Package.Round++
		todo, versions := c.todo(logger, candidate, inFlight, moved, swept, now)
		if todo == workNone {
			continue
		}
		n, tried, err := c.runPackage(ctx, logger, input, candidate, input.Limit-attempted, todo, versions, cfg.Breadth, inFlight)
		if err != nil && tried == 0 {
			// A package that failed before reaching any version still cost API calls
			// and still has to move the run forward, or a failure every package
			// shares walks the whole registry.
			tried = 1
		}
		attempted += tried
		if err != nil {
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
func (c *Controller) todo(logger *slog.Logger, candidate *Candidate, inFlight, moved map[string]struct{}, swept map[string][]string, now time.Time) (work, []string) {
	if _, ok := moved[candidate.Name]; ok {
		// Its branch is under another name now, and so is what the state says about
		// it. Generating here would generate under a name nothing holds.
		logger.Debug("the package was moved to another name this run", "package", candidate.Name)
		return workNone, nil
	}
	if head, ok := c.g2.HeadBranch(candidate.Name); ok {
		if _, inFlight := inFlight[head]; inFlight {
			// A pull request for this package is still open. Opening a second one
			// would write the same versions.json and conflict on merge, and if the
			// first is open because its CI failed, a copy of it helps no one.
			logger.Debug("skipping a package with an open pull request", "package", candidate.Name)
			return workNone, nil
		}
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
func (c *Controller) candidates(logger *slog.Logger, input *Input, cfg *g2.RegistryConfig) []*Candidate {
	// Dropped before the sweep, so that a repository nobody expects to answer isn't
	// asked about either.
	candidates := rejoin(logger, ignore(logger, order(input.State), cfg.Ignored()))
	return only(logger, candidates, input.Packages)
}

// only narrows the run to the packages it was asked for, in the order it would have
// reached them.
//
// The order is kept rather than the order they were named in, because what the order
// decides is which package has waited longest, and naming a few doesn't change that
// between them.
//
// A name that isn't among the candidates is said rather than ignored. It is a package
// aqua-registry doesn't have -- 'ar2 add' is what puts one in the order -- or one the
// registry is told to leave alone, and either way nothing would happen for it.
func only(logger *slog.Logger, candidates []*Candidate, pkgNames []string) []*Candidate {
	if len(pkgNames) == 0 {
		return candidates
	}
	asked := make(map[string]struct{}, len(pkgNames))
	for _, pkgName := range pkgNames {
		asked[pkgName] = struct{}{}
	}
	out := make([]*Candidate, 0, len(pkgNames))
	for _, candidate := range candidates {
		if _, ok := asked[candidate.Name]; !ok {
			continue
		}
		delete(asked, candidate.Name)
		out = append(out, candidate)
	}
	for pkgName := range asked {
		logger.Warn("the package isn't one this run could reach",
			"package", pkgName,
			"reason", "aqua-registry doesn't have it, or the registry is told to leave it alone")
	}
	return out
}

// rejoin puts a package that was outside the order back at the end of it.
//
// A run takes the packages with the fewest turns and gives each of them one, so the
// counts of the packages in the order are never more than one apart: a run works
// through the lowest count from the front, and whether it finishes that group or
// stops inside it, what is left is that count and the one above. Anything further
// behind than that wasn't in the order to be counted.
//
// Which happens when a package leaves the registry's ignored list. It is dropped
// before the order is formed, so its count stops while the rest of the registry goes
// on, and left as it was it would be first in every run until it caught up -- the
// thing counting turns exists to stop. A package the registry has just gained is the
// other way in, and joins at the back when it is added.
func rejoin(logger *slog.Logger, candidates []*Candidate) []*Candidate {
	if len(candidates) == 0 {
		return candidates
	}
	back := candidates[len(candidates)-1].Package.Round
	rejoined := false
	for _, candidate := range candidates {
		if back-candidate.Package.Round <= 1 {
			// Sorted by the count, so nothing after this one is further behind.
			break
		}
		logger.Info("a package was out of the order and rejoins at the end of it",
			"package", candidate.Name, "turns", candidate.Package.Round, "turns_of_the_rest", back)
		candidate.Package.Round = back
		rejoined = true
	}
	if rejoined {
		// The counts decide the order, so changing one changes it.
		slices.SortFunc(candidates, compare)
	}
	return candidates
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

// report writes what the run left out, what it moved, and how far the registry has got.
//
// Failing to say any of it isn't worth failing the run over: the versions are still in
// the log, and the run generated whatever else it could.
func (c *Controller) report(logger *slog.Logger, s *state.State) {
	if c.summary == nil {
		return
	}
	if s != nil {
		// After the run rather than before it, so the turns include the ones it took.
		if err := c.summary.Progress(s.Progress()); err != nil {
			slogerr.WithError(logger, err).Warn("write the summary of how far the registry has got")
		}
	}
	if err := c.summary.Problems(c.problems); err != nil {
		slogerr.WithError(logger, err).Warn("write the summary of what wasn't published")
	}
	if err := c.summary.Renames(c.renamed); err != nil {
		slogerr.WithError(logger, err).Warn("write the summary of what was renamed")
	}
}

// runPackage generates the missing versions of one package, newest first, up to
// budget. It returns how many were generated and how many were attempted.
func (c *Controller) runPackage(ctx context.Context, logger *slog.Logger, input *Input, candidate *Candidate, budget int, todo work, swept []string, breadth *g2.Breadth, inFlight map[string]struct{}) (int, int, error) {
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
	if err := c.checkRepoID(def, pkg.RepoOwner+"/"+pkg.RepoName); err != nil {
		return 0, 0, err
	}

	versions, err := c.candidateVersions(ctx, logger, input, candidate, def, todo, swept)
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

	generated, attempted := c.generateVersions(ctx, logger, input, def, candidate.Name, versions, existing,
		limitBudget(budget, existing, breadth))
	if len(generated) == 0 {
		return 0, attempted, nil
	}

	c.reviewLostSigning(ctx, logger, candidate.Name, generated, versions, existing)

	if input.SkipPR {
		return len(generated), attempted, writeAll(input.OutputDir, candidate.Name, generated)
	}

	if err := c.openPullRequests(ctx, logger, def, candidate.Name, generated, inFlight); err != nil {
		return 0, attempted, err
	}
	return len(generated), attempted, nil
}

// openPullRequests takes what a package's turn generated to the pull requests it belongs in.
//
// Two of them at most: the versions that can merge go to the package's own, and the ones whose
// entry names a file the archive doesn't hold go together to one of their own. Neither waits
// for the other, which is the point of telling them apart.
func (c *Controller) openPullRequests(ctx context.Context, logger *slog.Logger, def *definition, pkgName string, generated []*version, inFlight map[string]struct{}) error {
	sound, unresolved := partition(generated)
	if err := c.openUnresolved(ctx, logger, def, pkgName, unresolved, inFlight); err != nil {
		return err
	}
	// After the pull requests, so that what the issue says is in the registry is on its
	// way there rather than only generated.
	defer c.reportExcluded(ctx, logger, pkgName, sound)
	if len(sound) == 0 {
		return nil
	}
	return c.openPullRequest(ctx, logger, def, pkgName, sound)
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

// share is how much of a run one package may take.
type share struct {
	// attempts is how many versions may be tried.
	attempts int
	// versions ends the turn early once that many have been generated, or is zero
	// when the package is past the breadth and takes what it can get.
	versions int
}

// limitBudget limits how much of the run's remaining budget one package may take.
//
// A package that the registry holds few enough versions of gets enough of a turn to
// reach the breadth, so the run moves on instead of finishing this one. A package
// already past it takes whatever is left.
//
// The two bounds are separate because the versions likeliest to fail are the newest,
// which are the ones a turn starts from: a release with no assets, or one whose
// signature can't be verified yet, produces nothing. Counting only the tries leaves a
// package short of the breadth every turn and taking one every lap forever; counting
// only what worked would let a package whose every version fails spend the whole run.
//
// A registry that asks for no attempts of its own gets as many as it wants and no
// more, which is one try per version per lap.
func limitBudget(budget int, existing map[string]struct{}, breadth *g2.Breadth) *share {
	want := breadth.GetVersions() - len(existing)
	if want <= 0 {
		return &share{attempts: budget}
	}
	attempts := want
	if a := breadth.GetAttempts(); a > 0 {
		attempts = a
	}
	return &share{
		attempts: min(budget, attempts),
		versions: want,
	}
}

// generateVersions generates the versions the repository is missing, newest first,
// within the share of the run the package has.
func (c *Controller) generateVersions(ctx context.Context, logger *slog.Logger, input *Input, def *definition, pkgName string, versions []string, existing map[string]struct{}, budget *share) ([]*version, int) {
	generated := make([]*version, 0, budget.attempts)
	attempted := 0
	for _, tag := range versions {
		if attempted >= budget.attempts {
			return generated, attempted
		}
		if budget.versions > 0 && len(generated) >= budget.versions {
			// The package has what this turn was for, and the rest of its history
			// is what the next turn is for.
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

// repos is what a package's releases are read from: the instance's client when the
// definition says the package is on one, and nil otherwise, which the generator reads as
// GitHub.
func (c *Controller) repos(def *definition, base *aquaregistry.PackageInfo) genrgst.RepositoriesService {
	info := instanceOf(def, base)
	if info == nil {
		return nil
	}
	return forge.For(c.httpClient, info.Type, info.GetHost())
}

// generate builds registry.json for one package version and completes it.
func (c *Controller) generate(ctx context.Context, logger *slog.Logger, input *Input, def *definition, pkgName, tag string) (*version, error) {
	reg, err := c.generator.Generate(ctx, logger, &generate.Input{
		PkgName: pkgName,
		Version: tag,
		Base:    input.PkgInfos[pkgName],
		Config:  def.config,
		Repos:   c.repos(def, input.PkgInfos[pkgName]),
	})
	if err != nil {
		return nil, fmt.Errorf("generate registry.json: %w", err)
	}
	filled, err := c.verifier.Complete(ctx, logger, pkgName, tag, reg, input.Verify)
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
		sign.Structure(asset, tag)
	}
	generate.StampRepoID(reg, def.config)
	v := &version{
		Version:     tag,
		Registry:    reg,
		NeedsReview: filled.NeedsReview || dropped,
		Excluded:    filled.Excluded,
	}
	if filled.Offered == 0 {
		// Nothing to publish: what the file would say is that the registry holds this
		// version for nowhere. Such a version goes to the pull request of the ones
		// waiting for a definition, carrying what was generated for it.
		v.NeedsReview = true
		v.Unresolved = filled.Unresolved
	}
	return v, nil
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

// write stores registry.json at the path it has in the package's directory.
func write(dir, pkgName, version string, reg *generate.Registry) error {
	// The layout mirrors aqua-registry-g2: the package's directory holds
	// versions/<version>/registry-1.json, and the package name is a directory here so
	// that one run's output holds more than one package.
	path := filepath.Join(dir, filepath.Join(strings.Split(pkgName, "/")...), filepath.FromSlash(aquag2.Path(version)))
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create a directory for registry.json: %w", err)
	}
	b, err := generate.Marshal(reg)
	if err != nil {
		return err //nolint:wrapcheck // the error says what it couldn't marshal
	}
	if err := os.WriteFile(path, b, filePerm); err != nil {
		return fmt.Errorf("write registry.json: %w", err)
	}
	return nil
}

const dirPerm = 0o750

// filePerm is the mode registry.json is written with.
const filePerm = 0o644

// partition splits the versions into the ones that can merge and the ones that have to wait
// for a definition.
func partition(versions []*version) (sound, unresolved []*version) {
	for _, v := range versions {
		if len(v.Unresolved) > 0 {
			unresolved = append(unresolved, v)
			continue
		}
		sound = append(sound, v)
	}
	return sound, unresolved
}

// autoMerger is what turns auto-merge on: the one it was told, or the reading client, which
// is what a run with no app token has.
func (c *Controller) autoMerger() AutoMerger {
	if c.autoMerge != nil {
		return c.autoMerge
	}
	return c.graphql
}
