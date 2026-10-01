// Package versions implements the 'ar2 versions' command.
package versions

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/identities"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/versions"
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
	Branches []string
	Limit    int
	DryRun   bool
	G2Owner  string
	G2Repo   string
}

// New creates the 'ar2 versions' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "versions [<package name>...]",
		Short: "Write the list of versions each package branch holds",
		Long: `Write the list of versions each package branch holds.

A package's versions are the directories on its branch, so listing them means reading the
branch's tree and then a file per version to learn anything about them. versions.json is
that list as one object, with the date each release was published and the digest of the
file the registry serves.

$ ar2 versions
$ ar2 versions cli/cli
$ ar2 versions --branch pkg_1790772767
$ ar2 versions --dry-run

Nothing here decides what the list says: it is the branch's own directories and the files
in them. So it is pushed onto the package branch rather than opened as a pull request,
with the app that bypasses the pull request requirement, which is AR2_BRANCH_TOKEN.

A branch whose list is already the list is left alone, so a run over a registry that is up
to date writes nothing. What isn't compared is the commit the list says it came from: that
moves whenever anything on the branch does, and comparing it would write every list again
on every run.

--branch is for a branch's own workflow, which knows the branch it is running on and not
the package it holds.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, pkgNames []string) error {
			return action(cmd.Context(), logger, args, pkgNames)
		},
	}
	fs := cmd.Flags()
	fs.StringSliceVar(&args.Branches, "branch", nil, "the package branches to write, instead of naming packages")
	fs.IntVar(&args.Limit, "limit", 0, "how many branches one run writes to, or 0 for as many as there are")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which lists would change without writing anything")
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
	registry := g2.New(gh, branchGH, nil, args.G2Owner, args.G2Repo, args.Version)

	// A run told which branch to write needs no table: the branch is the id, and what the
	// list says is read from the branch itself. Naming packages is what has to be
	// resolved, and so is naming nothing, which is every package.
	defs := map[string]string{}
	if len(args.Branches) == 0 {
		httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))
		_, files, err := identities.Read(ctx, logger.Logger, registry, httpClient, args.G2Owner, args.G2Repo)
		if err != nil {
			return err //nolint:wrapcheck
		}
		defs = files
	}

	c := ctrl.New(registry, defs)
	return c.Write(ctx, logger.Logger, &ctrl.Args{ //nolint:wrapcheck
		Packages: pkgNames,
		Branches: args.Branches,
		Limit:    args.Limit,
		DryRun:   args.DryRun,
	})
}
