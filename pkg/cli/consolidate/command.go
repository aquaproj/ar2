// Package consolidate implements the 'ar2 consolidate' command.
package consolidate

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/consolidate"
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
	DryRun  bool
	G2Owner string
	G2Repo  string
}

// New creates the 'ar2 consolidate' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Move every package from its own branch onto the default branch",
		Long: `Move every package from its own branch onto the default branch.

Each package was kept on an orphan branch, pkg_<id>. From now on it is kept under
pkgs/<last two digits of id>/<id>/ on the default branch, and this opens the one pull
request that moves them all.

$ ar2 consolidate --dry-run
$ ar2 consolidate

Each package's registry.yaml, versions.json and versions/ are copied as the objects its
branch already holds, by their sha: nothing is uploaded, and what every version serves
is the same bytes it served from the branch. Running it again writes the same tree.

Two tokens are read from the environment. GITHUB_TOKEN reads the repository, and
AR2_PR_TOKEN commits and opens the pull request.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.DryRun, "dry-run", false, "say what would move without writing anything")
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
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck // the error already names the token it is for
	}
	registry := g2.New(gh, prGH, args.G2Owner, args.G2Repo, args.Version)
	return ctrl.New(registry).Consolidate(ctx, logger.Logger, args.DryRun) //nolint:wrapcheck
}
