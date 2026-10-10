// Package rewrite implements the 'ar2 rewrite' command.
package rewrite

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/rewrite"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

var (
	errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")
	errRefsRequired  = errors.New("--verify needs --base and --head")
)

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	DryRun  bool
	Verify  bool
	Base    string
	Head    string
	G2Owner string
	G2Repo  string
}

// New creates the 'ar2 rewrite' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "rewrite",
		Short: "Write the published files the way a generation writes them now",
		Long: `Write the published files the way a generation writes them now.

Run once (aquaproj/aqua-registry-g2#743). Each definition gains its repository's id, and
every registry.json is rendered again: the id on the entries naming that repository,
cosign's command line moved into the fields that say it, indented. Each versions.json is
written again, since every digest changes. It opens one pull request.

$ ar2 rewrite --dry-run
$ ar2 rewrite

Nothing about a release is read again, so the pull request is checked by deriving it
again rather than by downloading every asset:

$ ar2 rewrite --verify --base <base sha> --head <head sha>

GITHUB_TOKEN reads the repository, and AR2_PR_TOKEN commits and opens the pull request.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.DryRun, "dry-run", false, "say what would be rewritten without writing anything")
	fs.BoolVar(&args.Verify, "verify", false, "check that --head is --base rewritten, instead of rewriting")
	fs.StringVar(&args.Base, "base", "", "with --verify, the commit the pull request is based on")
	fs.StringVar(&args.Head, "head", "", "with --verify, the pull request's head commit")
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
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))
	graphql := github.NewClient(httpClient)
	repo := graphql.Repository(args.G2Owner, args.G2Repo)
	if args.Verify {
		if args.Base == "" || args.Head == "" {
			return errRefsRequired
		}
		return ctrl.New(repo, graphql, nil).Verify(ctx, logger.Logger, args.Base, args.Head) //nolint:wrapcheck
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(ghToken))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	writer := g2.New(gh, prGH, args.G2Owner, args.G2Repo, args.Version)
	return ctrl.New(repo, graphql, writer).Rewrite(ctx, logger.Logger, args.DryRun) //nolint:wrapcheck
}
