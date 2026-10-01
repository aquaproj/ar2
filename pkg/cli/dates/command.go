// Package dates implements the 'ar2 dates' command.
package dates

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/identities"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/dates"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Limit   int
	DryRun  bool
	G2Owner string
	G2Repo  string
}

// New creates the 'ar2 dates' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "dates [<package name>...]",
		Short: "Fill in when the release each held version came from was published",
		Long: `Fill in when the release each held version came from was published.

registry.json says it from ar2 v0.5.0 on, and the files written before that don't. Nothing
can work it out from them: the version string doesn't say it, and a package whose tags
aren't semver has nothing else to order its releases by.

$ ar2 dates
$ ar2 dates cli/cli
$ ar2 dates --dry-run

Nothing is generated again. No asset is downloaded, no definition is consulted, and
nothing already in a file is touched -- the one field is added and the file is written the
way a generation writes it, so the same version generated again comes out the same bytes.

That is why this pushes onto the package branches instead of opening a pull request each.
A pull request would be one per package asserting what its own diff proves, and the
release it was read from is what says whether it is right. The push needs the app that
bypasses the pull request requirement, which is AR2_BRANCH_TOKEN.

A package whose versions are its tags is skipped: its versions aren't releases, so the
release list doesn't hold them. So is one that isn't released on GitHub.

Each package costs one release listing, which answers for a hundred versions, and stops as
soon as the versions asked about have been found. A package already filled in costs the
reads that establish so and no write, so running this twice is no worse than running it
once.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, pkgNames []string) error {
			return action(cmd.Context(), logger, args, pkgNames)
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&args.Limit, "limit", 0, "how many packages one run writes to, or 0 for as many as there are")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which versions would be dated without writing anything")
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
	// The branch token is what pushes: its app is the one that bypasses the pull request
	// requirement on the package branches.
	branchGH, err := token.Client(token.BranchEnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))

	registry := g2.New(gh, branchGH, nil, args.G2Owner, args.G2Repo, args.Version)
	// The definitions come back with the table they were read out of, and they are what
	// says where each package's releases are.
	_, defs, err := identities.Read(ctx, logger.Logger, registry, httpClient, args.G2Owner, args.G2Repo)
	if err != nil {
		return err //nolint:wrapcheck
	}

	c := ctrl.New(registry, gh.Repositories, defs)
	return c.Fill(ctx, logger.Logger, &ctrl.Args{ //nolint:wrapcheck
		Packages: pkgNames,
		Limit:    args.Limit,
		DryRun:   args.DryRun,
	})
}
