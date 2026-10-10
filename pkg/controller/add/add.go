// Package add puts a package the registry doesn't have into it.
//
// Everything else a run does is about a package it already knows: the order comes from
// the state, and the state is built from aqua-registry's list. A package aqua-registry
// doesn't have -- one somebody asked for in an issue -- has no way in at all, which is
// what this is.
//
// Two things make a package known. Its definition, in its own directory, which is what
// generating reads; and its place in the order, which is what decides when it is
// generated. Neither follows from the other, so both are written here, and either can
// already be there: this is run again after a failure rather than unpicked.
package add

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
	gogithub "github.com/google/go-github/v92/github"
)

// Registry is what a package is added to.
type Registry interface {
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
	PackageBase(ctx context.Context, pkgName string) (string, error)
	// HeadBranch is the branch the pull request is opened from. It answers false for a
	// package the registry doesn't hold, which is every package until PackageBase has
	// minted the id it is kept under.
	HeadBranch(pkgName string) (string, bool)
	CommitPackage(ctx context.Context, logger *slog.Logger, pkgName, branch, parent, message string, files []*g2.File) error
	CreatePullRequest(ctx context.Context, logger *slog.Logger, pkgName, title, body string) (*gogithub.PullRequest, error)
}

// Repositories reads the repository the package comes from, for the description the
// catalogue lists it under.
type Repositories interface {
	Get(ctx context.Context, owner, repo string) (*gogithub.Repository, *gogithub.Response, error)
}

// Controller adds packages.
type Controller struct {
	g2    Registry
	repos Repositories
}

// New creates a Controller.
func New(registry Registry, repos Repositories) *Controller {
	return &Controller{g2: registry, repos: repos}
}

// Input is the package to add.
type Input struct {
	// PkgName is the aqua package name, which is the repository for most packages and
	// a command inside one for the rest: kubernetes/kubernetes/kubectl.
	PkgName string
	// Repo is the repository, as "<owner>/<name>". Empty reads it from PkgName.
	Repo string
	// Commands are the executables the package installs. Empty means one named after
	// the last part of PkgName, which is what the release is inferred to hold.
	Commands []string
	// State is the order the package joins.
	State *state.State
	// DryRun renders the definition and writes nothing.
	DryRun bool
}

// Add writes the package's definition and puts it into the order.
//
// It reports whether the state changed, which is what says the caller has to store it.
// The definition goes first: a package in the order whose branch holds no definition is
// reached by a run that then has nothing to generate from, while a definition nothing
// has ordered simply waits.
func (c *Controller) Add(ctx context.Context, logger *slog.Logger, in *Input) (bool, error) {
	owner, name, err := c.repo(in)
	if err != nil {
		return false, err
	}
	logger = logger.With("package", in.PkgName)

	cfg, err := c.definition(ctx, logger, in, owner, name)
	if err != nil {
		return false, err
	}
	if in.DryRun {
		rendered, err := marshal(cfg)
		if err != nil {
			return false, err
		}
		logger.Info("the definition the package would be added with", "definition", "\n"+rendered)
		return false, nil
	}
	if cfg != nil {
		if err := c.openPullRequest(ctx, logger, in.PkgName, cfg); err != nil {
			return false, err
		}
	}
	return c.join(logger, in, owner, name), nil
}

// repo is the repository the package's versions come from.
func (c *Controller) repo(in *Input) (string, string, error) {
	repo := in.Repo
	if repo == "" {
		repo = in.PkgName
	}
	parts := strings.SplitN(repo, "/", 3) //nolint:mnd
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: %s", errNoRepo, repo)
	}
	return parts[0], parts[1], nil
}

// definition builds the definition to commit, or nil when the branch already holds one.
//
// A branch that has one is a package that was added before, or one added by a run
// converting aqua-registry's. Writing over it would replace a definition somebody
// reviewed with one inferred from a name.
func (c *Controller) definition(ctx context.Context, logger *slog.Logger, in *Input, owner, name string) (*aquag2.Config, error) {
	held, err := c.g2.Config(ctx, in.PkgName)
	if err != nil {
		return nil, fmt.Errorf("get the package definition: %w", err)
	}
	if held != nil {
		logger.Info("the registry already holds a definition of the package")
		return nil, nil //nolint:nilnil
	}
	return c.newDefinition(ctx, logger, in, owner, name), nil
}

// newDefinition is the definition a package starts with.
//
// As little as possible. What the assets are called, which of them belongs to which
// environment, what format they are in and what is inside them are read from the
// release, so a definition that said any of it would be a copy of something that can
// be looked at. What a release doesn't say is which of its files is the command, which
// is why that is the one thing asked for.
func (c *Controller) newDefinition(ctx context.Context, logger *slog.Logger, in *Input, owner, name string) *aquag2.Config {
	pkgInfo := &aquaregistry.PackageInfo{
		// The package the directory holds. A directory is named after an id, so the
		// definition is the only thing that says which package it is: the catalogue
		// is built by reading it, and a definition that says no name is one
		// validate-definition refuses.
		Name:      in.PkgName,
		Type:      aquaregistry.PkgInfoTypeGitHubRelease,
		RepoOwner: owner,
		RepoName:  name,
	}
	for _, command := range commands(in) {
		pkgInfo.Files = append(pkgInfo.Files, &aquaregistry.File{Name: command})
	}
	description, repoID := c.repository(ctx, logger, owner, name)
	if description != "" {
		pkgInfo.Description = description
	}
	return &aquag2.Config{PackageInfo: pkgInfo, RepoID: repoID}
}

// commands are the executables the package installs.
func commands(in *Input) []string {
	if len(in.Commands) > 0 {
		return in.Commands
	}
	// The last part of the package name, which is the repository for most packages and
	// the command itself for a package that is one command of a repository holding
	// several. It is also what the inference guesses, so saying it changes nothing and
	// leaving it out would make the definition say nothing at all.
	parts := strings.Split(in.PkgName, "/")
	return []string{parts[len(parts)-1]}
}

// repository is what the catalogue lists the package under, and the repository's id.
//
// The description is read from the repository rather than asked for: it is a sentence
// somebody has already written, and a registry of thousands of packages is not improved by
// each of them being described again by whoever added it. The id is what a run holds the
// package's repository to from then on, since a name can be taken over and an id can't.
//
// A repository that can't be read leaves both out. The catalogue can carry a package with
// no description, and a run records the id once it reads the repository.
func (c *Controller) repository(ctx context.Context, logger *slog.Logger, owner, name string) (string, int64) {
	repo, _, err := c.repos.Get(ctx, owner, name)
	if err != nil {
		logger.Warn("failed to read the repository for its description", "error", err.Error())
		return "", 0
	}
	return strings.TrimSuffix(strings.TrimSpace(repo.GetDescription()), "."), repo.GetID()
}

// join puts the package into the order, and reports whether it wasn't there already.
func (c *Controller) join(logger *slog.Logger, in *Input, owner, name string) bool {
	if _, ok := in.State.Packages[in.PkgName]; ok {
		logger.Info("the package is in the order already")
		return false
	}
	round := in.State.Front()
	logger.Info("putting the package into the order", "turns", round)
	in.State.Packages[in.PkgName] = &state.Package{
		RepoOwner: owner,
		RepoName:  name,
		Round:     round,
	}
	return true
}

// openPullRequest commits the definition and opens the pull request that brings it.
//
// No auto-merge, whatever CI says about it. A pull request carrying no generated file
// has nothing for CI to check -- the checks read the files an entry describes, and there
// are none yet -- and the definition is the one thing here a person writes, deciding
// everything generated from it afterwards.
func (c *Controller) openPullRequest(ctx context.Context, logger *slog.Logger, pkgName string, cfg *aquag2.Config) error {
	base, err := c.g2.PackageBase(ctx, pkgName)
	if err != nil {
		return fmt.Errorf("take the package over: %w", err)
	}
	// After the base, because that is what mints the id the head branch is named after.
	head, ok := c.g2.HeadBranch(pkgName)
	if !ok {
		return fmt.Errorf("%w: %s", errNoBranch, pkgName)
	}
	content, err := marshal(cfg)
	if err != nil {
		return err
	}
	title := "feat(" + pkgName + "): add the package"
	if err := c.g2.CommitPackage(ctx, logger, pkgName, head, base, title, []*g2.File{
		{Path: g2.ConfigFileName, Content: content},
	}); err != nil {
		return fmt.Errorf("commit the package definition: %w", err)
	}
	pr, err := c.g2.CreatePullRequest(ctx, logger, pkgName, title, body(cfg))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened a pull request", "number", pr.GetNumber())
	return nil
}

// body says what the pull request is and what happens once it merges.
func body(cfg *aquag2.Config) string {
	var b strings.Builder
	b.WriteString("Adds the definition of a package the registry doesn't have yet.\n\n")
	b.WriteString("It says as little as it can. What the assets are called, which environment each one is " +
		"for, what format they are in and what is inside them are read from the release when a version is " +
		"generated, so a definition that said any of it would be a copy of something that can be looked at. " +
		"What a release doesn't say is which of its files is the command:\n\n")
	for _, file := range cfg.Files {
		b.WriteString("- `" + file.Name + "`\n")
	}
	b.WriteString("\nSo that is what to check, along with the repository being the one the issue asked for. " +
		"Nothing is generated yet, and CI has nothing to check here: the checks read the files an entry " +
		"describes and there are none until a run reaches the package, which is the next thing that happens.\n")
	return b.String()
}
