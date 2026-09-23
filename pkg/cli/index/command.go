// Package index implements the 'ar2 index' command.
package index

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/index"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
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
		Use:   "index",
		Short: "Add the packages missing from aqua-registry-g2's index.json",
		Long: `Add the packages missing from aqua-registry-g2's index.json.

index.json is what 'aqua g' searches: it holds the name, description and link of
every package the registry has. Everything else about a package lives on its own
branch, but searching reads all of them at once, so the catalogue is one file on the
default branch and has to be kept in step with the branches.

'ar2 run' adds a package as it takes it over, so this command is the reconciliation
rather than the ordinary path. Every package branch is listed and whatever the
catalogue doesn't have is added, which is what catches a package whose run failed
after committing its definition or whose pull request was never merged. Nothing else
would notice. It belongs on a schedule.

$ ar2 index

The update goes into one pull request, and a run finding one already open adds to it
rather than opening another.

The GitHub access token is read from the GITHUB_TOKEN environment variable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

	// Auto-merge is turned on with the ordinary token: it needs the pull request the
	// app just opened, not the app.
	c := ctrl.New(g2.New(gh, nil, prGH, args.G2Owner, args.G2Repo),
		github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))),
		args.BaseBranch)
	return c.Sync(ctx, logger.Logger) //nolint:wrapcheck
}
