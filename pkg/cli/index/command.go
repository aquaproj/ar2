// Package index implements the 'ar2 index' command.
package index

import (
	"context"
	"errors"
	"fmt"
	"os"

	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"github.com/szksh-lab-2/ar2/pkg/cli/flag"
	ctrl "github.com/szksh-lab-2/ar2/pkg/controller/index"
	"github.com/szksh-lab-2/ar2/pkg/g2"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Branch     string
	BaseBranch string
	G2Owner    string
	G2Repo     string
}

// New creates the 'ar2 index' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	cmd := &cobra.Command{
		Use:   "index [<package branch>]",
		Short: "Add packages to aqua-registry-g2's index.json",
		Long: `Add packages to aqua-registry-g2's index.json.

index.json is what 'aqua g' searches: it holds the name, description and link of
every package the registry has. Everything else about a package lives on its own
branch, but searching reads all of them at once, so the catalogue is one file on the
default branch and has to be kept in step with the branches.

Given a package branch, only that package is looked at. That is the form a workflow
watching for branch creation uses, and it costs one request: the branch that was just
created is the only one that can be missing.

$ ar2 index pkg_cli_2fcli

Without an argument every package branch is listed and whatever the catalogue doesn't
have is added. That is what catches a package the other form missed, which happens
whenever the event didn't fire or its pull request never merged. Nothing else would
notice.

$ ar2 index

Either way the update goes into one pull request, and a run finding one already open
adds to it rather than opening another.

The GitHub access token is read from the GITHUB_TOKEN environment variable.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			if len(positional) > 0 {
				args.Branch = positional[0]
			}
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the pull request targets")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errTokenRequired
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(token))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}

	c := ctrl.New(g2.New(gh, nil, args.G2Owner, args.G2Repo), args.BaseBranch)
	if args.Branch == "" {
		return c.Sync(ctx, logger.Logger) //nolint:wrapcheck
	}
	return c.Add(ctx, logger.Logger, args.Branch) //nolint:wrapcheck
}
