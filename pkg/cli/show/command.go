// Package show implements the 'ar2 show' command.
package show

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	ctrl "github.com/aquaproj/ar2/pkg/controller/show"
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
	BaseBranch string
	G2Owner    string
	G2Repo     string
}

// New creates the 'ar2 show' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "show <package name|id>",
		Short: "Say what the registry knows about one package",
		Long: `Say what the registry knows about one package.

A package's branch is named after an identifier rather than after the package, so neither
can be read off the other. A name in a pull request title, a branch in a log, an
identifier in a lock file: each one leaves the others to be looked up, and the catalogue
is where all of it is.

$ ar2 show cli/cli
$ ar2 show 1790000000

A name the package used to have answers too, because that is often the name in hand.

It reads the catalogue and nothing else, so it costs one request and says nothing about
which versions the registry holds.

The GitHub access token is read from the GITHUB_TOKEN environment variable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, queries []string) error {
			return action(cmd.Context(), logger, args, cmd.OutOrStdout(), queries[0])
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the catalogue is read from")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, out io.Writer, query string) error {
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
	c := ctrl.New(g2.New(gh, nil, nil, args.G2Owner, args.G2Repo, args.Version), args.BaseBranch)
	return c.Show(ctx, out, query) //nolint:wrapcheck // the error already names what it failed to find
}
