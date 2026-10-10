// Package tidy implements the 'ar2 tidy' command.
package tidy

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/identities"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/tidy"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// defaultLimit bounds how many pull requests one run opens.
//
// A change to every definition is a pull request per package, and a run that opened eighty at
// once would be a day's reviewing arriving at once.
const defaultLimit = 20

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Limit   int
	DryRun  bool
	G2Owner string
	G2Repo  string
}

// New creates the 'ar2 tidy' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "tidy [<package name>...]",
		Short: "Take out of the definitions what a release can be read for",
		Long: `Take out of the definitions what a release can be read for.

A definition should say the part a release can't answer: which assets are the command,
which of its files is the executable, who signs it. Everything else is read off the
release on every generation, and a definition repeating it is a copy that can go out of
date while nobody touches it.

The replacements are that. They say how the release writes a platform -- darwin as osx,
amd64 as x86_64 -- and the parser reads almost all of those off the asset names for
itself. What it can't read stays: luau-lang/luau calls its Linux build luau-ubuntu.zip,
and the replacement is the only thing that makes that asset Linux at all.

$ ar2 tidy
$ ar2 tidy astral-sh/uv
$ ar2 tidy --dry-run

The conversion has written definitions this way since v0.0.27, so this is for the ones
written before it. Nothing generated changes, which was measured rather than assumed, so
the pull requests merge themselves once their checks pass.

A pull request per package, so that each merges on its own checks. --limit bounds how
many one run opens, and a package with a pull request already open is left for a later
run: committing would reset the branch that one is on.

The definitions are read in one query for the whole registry rather than one per package,
so a run that finds nothing to do costs almost nothing.

Two tokens are read from the environment. GITHUB_TOKEN reads the repository, and
AR2_PR_TOKEN commits and opens the pull requests.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, pkgNames []string) error {
			return action(cmd.Context(), logger, args, pkgNames)
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&args.Limit, "limit", defaultLimit, "how many pull requests one run opens, or 0 for as many as there are")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which definitions would change without writing anything")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, pkgNames []string) error {
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
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))
	graphql := github.NewClient(httpClient)

	registry := g2.New(gh, prGH, args.G2Owner, args.G2Repo, args.Version)
	// The definitions come back with the table they were read out of, which is what the
	// tidy works through.
	_, files, err := identities.Read(ctx, logger.Logger, registry, httpClient, args.G2Owner, args.G2Repo)
	if err != nil {
		return err //nolint:wrapcheck
	}

	// Auto-merge is turned on with the ordinary token: it needs the pull request the app
	// just opened, not the app.
	c := ctrl.New(registry, files, graphql)
	return c.Tidy(ctx, logger.Logger, &ctrl.Args{ //nolint:wrapcheck
		Packages: pkgNames,
		Limit:    args.Limit,
		DryRun:   args.DryRun,
	})
}
