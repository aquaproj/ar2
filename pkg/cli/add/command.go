// Package add implements the 'ar2 add' command.
package add

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/statefile"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/add"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Repo       string
	Commands   []string
	DryRun     bool
	StateFile  string
	Registry   string
	Repository string
	Username   string
	G2Owner    string
	G2Repo     string
}

// Flags returns the settings locating the state in the container registry.
func (a *Args) Flags() *state.Flags {
	return &state.Flags{
		Registry:   a.Registry,
		Repository: a.Repository,
		Username:   a.Username,
	}
}

// New creates the 'ar2 add' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "add <package name>",
		Short: "Put a package the registry doesn't have into it",
		Long: `Put a package the registry doesn't have into it.

Everything else ar2 does is about a package it already knows. The order comes from the
state, and the state is built from aqua-registry's list, so a package aqua-registry
doesn't have -- one somebody asked for in an issue -- has no way in at all.

$ ar2 add cli/cli
$ ar2 add kubernetes/kubernetes/kubectl --repo kubernetes/kubernetes --command kubectl
$ ar2 add cli/cli --dry-run

Two things make a package known, and this writes both. Its definition, on its own
branch, which is what generating reads; and its place in the order, which decides when
it is generated. Neither follows from the other, and either may be there already, so
this is run again after a failure rather than unpicked.

The definition says as little as it can. What the assets are called, which environment
each one is for, what format they are in and what is inside them are read from the
release, so a definition that said any of it would be a copy of something that can be
looked at. What a release doesn't say is which of its files is the command, which is why
--command is the one thing asked for; without it the command is the last part of the
package name, which is what the release is inferred to hold anyway.

Nothing is generated here. The package joins the order at the current lap, so the next
'ar2 run' reaches it and opens the pull requests for its versions.

The pull request carrying the definition is never set to auto-merge. It carries no
generated file, so CI has nothing to check, and the definition is the one thing a person
writes.

The access token is read from GITHUB_TOKEN, and the state from the container registry
unless --state names a file. Committing needs AR2_BRANCH_TOKEN and AR2_PR_TOKEN, the
same as a run.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, as []string) error {
			return action(cmd.Context(), logger, args, as[0])
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.Repo, "repo", "", `the repository the versions come from, as "<owner>/<name>" (default the package name)`)
	fs.StringArrayVar(&args.Commands, "command", nil, "an executable the package installs, repeatable (default the last part of the package name)")
	fs.BoolVar(&args.DryRun, "dry-run", false, "render the definition and write nothing")
	fs.StringVar(&args.StateFile, "state", "", "read the state from this file instead of the container registry")
	fs.StringVar(&args.Registry, "registry", "ghcr.io", "the container registry the state is read from")
	fs.StringVar(&args.Repository, "repository", "", "the container registry repository (default $GITHUB_REPOSITORY)")
	fs.StringVar(&args.Username, "username", "", "the user the GitHub access token belongs to (default the owner of --repository)")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, pkgName string) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken == "" {
		return errTokenRequired
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(ghToken))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	branchGH, err := token.Client(token.BranchEnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck
	}

	s, err := statefile.Read(ctx, logger, args.Flags(), args.StateFile)
	if err != nil {
		return err //nolint:wrapcheck
	}

	c := ctrl.New(g2.New(gh, branchGH, prGH, args.G2Owner, args.G2Repo), gh.Repositories)
	changed, err := c.Add(ctx, logger.Logger, &ctrl.Input{
		PkgName:  pkgName,
		Repo:     args.Repo,
		Commands: args.Commands,
		State:    s,
		DryRun:   args.DryRun,
	})
	if err != nil {
		return fmt.Errorf("add the package: %w", err)
	}
	if !changed {
		return nil
	}
	// Stored after the definition rather than before it: a package in the order whose
	// branch holds nothing is reached by a run that has nothing to generate from.
	return statefile.Write(ctx, logger, args.Flags(), args.StateFile, s) //nolint:wrapcheck
}
