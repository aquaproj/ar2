// Package versions is the update-versions command.
package versions

import (
	"context"
	"errors"
	"fmt"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/versions"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

var errTokenRequired = errors.New("a GitHub access token is required: set GITHUB_TOKEN or set up ghtkn")

// Args is what the command takes.
type Args struct {
	*flag.GlobalFlags
	BaseBranch string
	DryRun     bool
	G2Owner    string
	G2Repo     string
}

// New returns the update-versions command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "update-versions [<package id>...]",
		Short: "Bring the packages' versions.json up to date with their versions",
		Long: `Bring the packages' versions.json up to date with their versions.

versions.json lists the versions a package's directory holds. A pull request adding a
version leaves it alone, because two of them open for one package would conflict on it, so
the list falls behind when one merges. This makes the lists behind again from the versions
each package's directory holds on the base branch.

$ ar2 update-versions

Which lists are behind is asked of every package, from what the base branch holds: a list
whose source isn't the sha of its versions directory. Nothing is remembered between runs, so
a run that didn't happen -- cancelled, or never raised by its push -- is caught by the next.
It costs two objects a package, read a hundred to a query.

Packages named by their ids are made again without asking the rest:

$ ar2 update-versions 1790772767 1791650998

Only the versions a list lacks are read, and the ones gone are taken out. A list that is
already in step with its directory is left alone.

The update goes into one pull request from ar2_versions, which merges itself once its
checks pass. A run finding one already open adds to it rather than opening another.

The GitHub access token is read from the GITHUB_TOKEN environment variable, or from
ghtkn when it isn't set. AR2_PR_TOKEN commits and opens the pull request.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, ids []string) error {
			return action(cmd.Context(), logger, args, ids)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the lists are made from and the pull request targets")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which lists are behind without committing anything")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, ids []string) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	ts, err := token.API(logger.Logger)
	if err != nil {
		return err //nolint:wrapcheck // the error already says what failed
	}
	if ts == nil {
		return errTokenRequired
	}
	httpClient := oauth2.NewClient(ctx, ts)
	gh, err := gogithub.NewClient(gogithub.WithHTTPClient(httpClient))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	// Auto-merge is turned on with the ordinary token: it needs the pull request the
	// app just opened, not the app.
	graphql := github.NewClient(httpClient)
	registry := g2.New(gh, prGH, args.G2Owner, args.G2Repo, args.Version)
	if len(ids) == 0 {
		stale, err := registry.StaleVersionsLists(ctx, logger.Logger, graphql.Repository(args.G2Owner, args.G2Repo))
		if err != nil {
			return err //nolint:wrapcheck // the error says what it couldn't read
		}
		if len(stale) == 0 {
			logger.Info("every list of versions is up to date")
			return nil
		}
		ids = stale
	}
	c := ctrl.New(registry, graphql, args.BaseBranch)
	if args.DryRun {
		c.DryRun()
	}
	return c.Update(ctx, logger.Logger, ids) //nolint:wrapcheck
}
