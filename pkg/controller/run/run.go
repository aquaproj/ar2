package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/szksh-lab-2/ar2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/state"
	"github.com/szksh-lab-2/ar2/pkg/verify"
)

// Controller runs the loop.
type Controller struct {
	gh        *gogithub.Client
	generator *generate.Generator
	g2        Registry
	graphql   AutoMerger
	verifier  *verify.Verifier
}

// Registry is aqua-registry-g2: what it holds, and how work is added to it.
type Registry interface {
	Versions(ctx context.Context, pkgName string) (map[string]struct{}, error)
	PackagesInFlight(ctx context.Context) (map[string]struct{}, error)
	EnsurePackageBranch(ctx context.Context, pkgName string) (string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	CreatePullRequest(ctx context.Context, pkgName, title, body string) (*gogithub.PullRequest, error)
}

// AutoMerger turns on auto-merge, which is what makes CI the gate: a pull request
// merges itself once the checks pass and stays open when they don't.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// New creates a Controller.
func New(gh *gogithub.Client, generator *generate.Generator, reg Registry, graphql AutoMerger, verifier *verify.Verifier) *Controller {
	return &Controller{gh: gh, generator: generator, g2: reg, graphql: graphql, verifier: verifier}
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
}

// Run generates registry.json for up to Limit package versions.
//
// It returns the number generated. Nothing is recorded: the next run asks
// aqua-registry-g2 what it holds, so whatever didn't make it in is picked up again.
func (c *Controller) Run(ctx context.Context, logger *slog.Logger, input *Input) (int, error) {
	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return 0, fmt.Errorf("list the packages with an open pull request: %w", err)
	}

	generated := 0
	for _, candidate := range order(input.State) {
		if generated >= input.Limit {
			return generated, nil
		}
		if _, ok := inFlight[g2.HeadBranchName(candidate.Name)]; ok {
			// A pull request for this package is still open. Opening a second one
			// would target the same package branch and conflict on merge, and if the
			// first is open because its CI failed, a copy of it helps no one.
			logger.Debug("skipping a package with an open pull request", "package", candidate.Name)
			continue
		}
		n, err := c.runPackage(ctx, logger, input, candidate, input.Limit-generated)
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

// runPackage generates the missing versions of one package, newest first, up to
// budget.
func (c *Controller) runPackage(ctx context.Context, logger *slog.Logger, input *Input, candidate *Candidate, budget int) (int, error) {
	pkg := candidate.Package
	if pkg.RepoOwner == "" || pkg.RepoName == "" {
		// Versions can only be listed for a GitHub repository. Packages without one
		// need another source and aren't handled yet.
		return 0, nil
	}
	versions, err := c.versions(ctx, logger, pkg, input.PkgInfos[candidate.Name])
	if err != nil {
		return 0, err
	}
	existing, err := c.g2.Versions(ctx, candidate.Name)
	if err != nil {
		return 0, fmt.Errorf("list the versions aqua-registry-g2 holds: %w", err)
	}

	budget = limitBudget(budget, existing)

	generated := c.generateVersions(ctx, logger, input, candidate.Name, versions, existing, budget)
	if len(generated) == 0 {
		return 0, nil
	}
	if input.SkipPR {
		return len(generated), writeAll(input.OutputDir, candidate.Name, generated)
	}
	if err := c.openPullRequest(ctx, logger, candidate.Name, generated); err != nil {
		return 0, err
	}
	return len(generated), nil
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
func (c *Controller) generateVersions(ctx context.Context, logger *slog.Logger, input *Input, pkgName string, versions []string, existing map[string]struct{}, budget int) []*version {
	generated := make([]*version, 0, budget)
	for _, tag := range versions {
		if len(generated) >= budget {
			return generated
		}
		if _, ok := existing[tag]; ok {
			continue
		}
		v, err := c.generate(ctx, logger, input, pkgName, tag)
		if err != nil {
			// A single version failing is normal: a release can have no assets at
			// all. The next run sees the version still missing and tries again.
			logger.Warn("failed to generate registry.json",
				"package", pkgName, "version", tag, "error", err.Error())
			continue
		}
		generated = append(generated, v)
	}
	return generated
}

// generate builds registry.json for one package version and completes it.
func (c *Controller) generate(ctx context.Context, logger *slog.Logger, input *Input, pkgName, tag string) (*version, error) {
	reg, err := c.generator.Generate(ctx, logger, &generate.Input{
		PkgName: pkgName,
		Version: tag,
		Base:    input.PkgInfos[pkgName],
	})
	if err != nil {
		return nil, fmt.Errorf("generate registry.json: %w", err)
	}
	needsReview, err := c.verifier.Fill(ctx, logger, tag, reg, input.Verify)
	if err != nil {
		return nil, fmt.Errorf("complete registry.json: %w", err)
	}
	return &version{Version: tag, Registry: reg, NeedsReview: needsReview}, nil
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
	// versions/<version>/registry.json, and the package name is a directory here so
	// that one run's output holds more than one package.
	path := filepath.Join(dir, filepath.Join(strings.Split(pkgName, "/")...), "versions", version, "registry.json")
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create a directory for registry.json: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create registry.json: %w", err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(reg); err != nil {
		return fmt.Errorf("write registry.json: %w", err)
	}
	return nil
}

const dirPerm = 0o750
