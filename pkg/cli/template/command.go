// Package template implements the 'ar2 template' command.
package template

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/template"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Branches []string
	Limit    int
	DryRun   bool
	G2Owner  string
	G2Repo   string
}

// New creates the 'ar2 template' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Write the template onto the package branches that hold something else",
		Long: `Write the template onto the package branches that hold something else.

A package branch is created holding the files in template/, and they are there rather than
on main because a workflow for a branch is read from that branch. They are copied once and
never updated, so each holds nothing but a call into main, where it can be improved for
every package at once.

What that leaves is a file added to the template afterwards, which the branches made before
it don't have, and a call that has to change. Both are this.

$ ar2 template
$ ar2 template --branch pkg_1790772767
$ ar2 template --dry-run

The template is compared by the blobs rather than the content: a git object belongs to the
repository, so a branch holding the template's blob holds the template's file, and the tree
that writes one names the blob the default branch holds rather than uploading it again. A
branch that is already right costs the one request that read its tree.

It pushes onto the package branches, which is AR2_BRANCH_TOKEN's bypass. The diff is a file
the registry itself wrote, and the pull request it would otherwise take would be one per
package.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.StringSliceVar(&args.Branches, "branch", nil, "the package branches to write, instead of every one")
	fs.IntVar(&args.Limit, "limit", 0, "how many branches one run writes to, or 0 for as many as there are")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which branches would be written to without writing")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
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
	// The branch token is what pushes: its app is the one that bypasses the pull request
	// requirement on the package branches.
	branchGH, err := token.Client(token.BranchEnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	// No table of names: what this addresses is branches, and the template says nothing
	// about which package a branch holds.
	registry := g2.New(gh, branchGH, nil, args.G2Owner, args.G2Repo, args.Version)

	c := ctrl.New(registry)
	return c.Sync(ctx, logger.Logger, &ctrl.Args{ //nolint:wrapcheck
		Branches: args.Branches,
		Limit:    args.Limit,
		DryRun:   args.DryRun,
	})
}
