// Package initcmd implements the logic behind the 'ar2 init' command.
//
// init builds ar2's initial state: it reads the package list from aqua-registry (v1),
// looks up each package's star count, and pushes the result to the container registry.
// The star count decides the order in which packages are processed later, because a
// full backfill can't be done in one run.
package initcmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/registry"
	"github.com/aquaproj/ar2/pkg/state"
	gogithub "github.com/google/go-github/v92/github"
)

// Controller runs 'ar2 init'.
type Controller struct {
	// gh reads the package list through the GitHub REST API.
	gh *gogithub.Client
	// graphql reads star counts. Star counts are read in batches, which the REST API
	// can't do.
	graphql *github.Client
	now     func() time.Time
}

// New creates a Controller. Both clients must carry the GitHub access token.
func New(gh *gogithub.Client, graphql *github.Client) *Controller {
	return &Controller{
		gh:      gh,
		graphql: graphql,
		now:     time.Now,
	}
}

// Input holds the parameters of 'ar2 init'.
type Input struct {
	// RegistryRef is the aqua-registry ref the package list is read from.
	RegistryRef string
	// GitHubToken authenticates the push to the container registry.
	GitHubToken string
	// Registry describes where the state artifact is pushed.
	Registry *state.Registry
	// Output writes the state to this path instead of pushing it. For debugging.
	Output string
}

// Init builds the initial state and pushes it to the container registry.
func (c *Controller) Init(ctx context.Context, logger *slog.Logger, input *Input) error {
	logger.Info("downloading the package list from aqua-registry", "ref", input.RegistryRef)
	reg, err := registry.Fetch(ctx, c.gh, input.RegistryRef)
	if err != nil {
		return fmt.Errorf("get the package list from aqua-registry: %w", err)
	}

	s, repos := newState(reg)
	logger.Info("read the package list", "num_of_packages", len(s.Packages), "num_of_repositories", len(repos))

	logger.Info("getting star counts", "num_of_requests", (len(repos)+github.BatchSize-1)/github.BatchSize)
	stars, reasons, err := c.graphql.GetStars(ctx, repos)
	if err != nil {
		return fmt.Errorf("get star counts: %w", err)
	}
	// An organization's IP allow list refuses an authenticated request even for a
	// public repository, and the GitHub Actions token counts, so running in Actions
	// loses the star count of every package such an organization owns. Anonymous
	// requests aren't covered by the allow list.
	c.graphql.FillForbiddenStars(ctx, stars, reasons)
	setStars(logger, s, stars, reasons)
	s.UpdatedAt = c.now()

	if input.Output != "" {
		logger.Info("writing the state to a file", "path", input.Output)
		if err := state.Write(input.Output, s); err != nil {
			return fmt.Errorf("write the state: %w", err)
		}
		return nil
	}
	return c.push(ctx, logger, input, s)
}

// newState turns the package list into the initial state and the list of
// repositories to look up.
func newState(reg *registry.Registry) (*state.State, []github.Repo) {
	s := state.New()
	repos := make([]github.Repo, 0, len(reg.Packages))
	// seen keeps one entry per repository: several packages can share a repository
	// (a monorepo publishing more than one binary), and their star count is the same.
	seen := map[string]struct{}{}
	for _, pkg := range reg.Packages {
		name := pkg.PackageName()
		if name == "" {
			continue
		}
		s.Packages[name] = &state.Package{
			RepoOwner: pkg.RepoOwner,
			RepoName:  pkg.RepoName,
		}
		if !pkg.HasRepo() {
			continue
		}
		repo := github.Repo{Owner: pkg.RepoOwner, Name: pkg.RepoName}
		if _, ok := seen[repo.String()]; ok {
			continue
		}
		seen[repo.String()] = struct{}{}
		repos = append(repos, repo)
	}
	return s, repos
}

// setStars copies the star counts into the state.
func setStars(logger *slog.Logger, s *state.State, stars map[string]int, reasons map[string]string) {
	for name, pkg := range s.Packages {
		if pkg.RepoOwner == "" {
			continue
		}
		repo := pkg.RepoOwner + "/" + pkg.RepoName
		star, ok := stars[repo]
		if !ok {
			// The repository was deleted, or an organization refused the request.
			// The package is kept so it is still processed, just last.
			logger.Warn("failed to get the star count",
				"package", name, "repo", repo, "reason", reasons[repo])
			continue
		}
		pkg.Stars = star
	}
}

// push writes the state to a temporary directory and uploads it.
// oras uploads files from a directory, so the state can't be pushed from memory.
func (c *Controller) push(ctx context.Context, logger *slog.Logger, input *Input, s *state.State) error {
	dir, err := os.MkdirTemp("", "ar2")
	if err != nil {
		return fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := state.Write(filepath.Join(dir, state.FileName), s); err != nil {
		return fmt.Errorf("write the state: %w", err)
	}

	repo, err := state.NewRepository(input.Registry, input.GitHubToken)
	if err != nil {
		return fmt.Errorf("create a client for the container registry: %w", err)
	}
	logger.Info("pushing the state to the container registry",
		"registry", input.Registry.Registry, "repository", input.Registry.Repository, "tag", state.Tag)
	if err := state.Push(ctx, repo, dir, state.Tag); err != nil {
		return fmt.Errorf("push the state: %w", err)
	}
	return nil
}
