// Package identify implements the 'ar2 identify' command.
package identify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/identify"
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
	G2Owner string
	G2Repo  string
	DryRun  bool
}

// New creates the 'ar2 identify' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "identify [<path>]",
		Short: "Put every package on the branch named after its id",
		Long: `Put every package on the branch named after its id.

A package's branch was named after the package, which made the name the address of
everything: the branch, the path aqua fetches a version from, the entry the catalogue
lists it under. A repository that is renamed then has to be moved everywhere, and
anything that wrote the old name down is left pointing at nothing. An id doesn't change
when a name does.

$ ar2 identify --dry-run
$ ar2 identify

The catalogue is the list of what to do, because the ids are in it. Each package's new
branch starts at the commit its old one is at, so nothing is copied and the record of
how each version arrived is still readable. Two corrections ride along in that commit,
since committing onto a package branch afterwards would take a pull request: the
definition is made to name its own package, which is how a branch named after an id
says what it holds, and a version directory written before a version was escaped into
one path segment is moved to the name it has now.

The old branches are left alone. A ruleset forbids deleting a package branch and no app
bypasses it, so removing them is a decision for whoever has the access.

This runs once, and a second run sees the branches the first made and has nothing to
do. Nothing is read from a branch it creates until the writes are switched over to it.

Two tokens are read from the environment. GITHUB_TOKEN reads the repository, and
AR2_BRANCH_TOKEN creates the branches, since that is what a ruleset requiring checks
lets through.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, as []string) error {
			path := g2.IndexFileName
			if len(as) == 1 {
				path = as[0]
			}
			return action(cmd.Context(), logger, args, cmd.OutOrStdout(), path)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.DryRun, "dry-run", false, "say what it would do and do nothing")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, w io.Writer, path string) error {
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

	registry := g2.New(gh, branchGH, nil, args.G2Owner, args.G2Repo, args.Version)
	return ctrl.New(registry).Identify(ctx, logger.Logger, w, path, args.DryRun) //nolint:wrapcheck
}
