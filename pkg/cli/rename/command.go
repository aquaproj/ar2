// Package rename implements the 'ar2 rename' command.
package rename

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	indexctrl "github.com/aquaproj/ar2/pkg/controller/index"
	ctrl "github.com/aquaproj/ar2/pkg/controller/rename"
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

// New creates the 'ar2 rename' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "rename <old package name> <new package name>",
		Short: "Move a package to the name it has now",
		Long: `Move a package to the name it has now.

A package's name is part of everything: the branch its generated versions live on, the
path aqua fetches them from, the entry the catalogue lists it under. A repository that
is renamed or transferred leaves all of it under a name nobody uses.

$ ar2 rename sst/opencode anomalyco/opencode

The branch is carried over rather than the versions generated again: each of them was
downloaded, hashed and opened on six machines to get there, and the new name is the
same history under another word for it. The old commit becomes the new branch's parent,
so nothing is copied.

The old name becomes an alias of the package, which is how a configuration still asking
for it resolves: aqua reads the aliases from the catalogue and the table beside it, and
fetches the package under the name it has now.

The old branch is left alone. A ruleset forbids deleting a package branch and no app
bypasses it, so removing it is a decision for whoever has the access. Nothing reads it
once the catalogue names the package under its new name.

'ar2 run' reports a repository that answers to another name, under "Renamed" in the job
summary. A rename GitHub can't see -- a package that changed names because one
repository started publishing several commands, say -- is this command and nothing else.

Three tokens are read from the environment. GITHUB_TOKEN reads the repository,
AR2_BRANCH_TOKEN creates the branch, since that is what a ruleset requiring checks lets
through, and AR2_PR_TOKEN opens the catalogue's pull request.`,
		Args: cobra.ExactArgs(2), //nolint:mnd
		RunE: func(cmd *cobra.Command, as []string) error {
			return action(cmd.Context(), logger, args, as[0], as[1])
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the pull request targets")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, from, to string) error {
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
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return err //nolint:wrapcheck
	}

	registry := g2.New(gh, branchGH, prGH, args.G2Owner, args.G2Repo)
	// No auto-merge: a rename is dispatched by a person, and the catalogue's pull
	// request is the last place it can be looked at before the old name stops being
	// listed.
	index := indexctrl.New(registry, nil, args.BaseBranch)
	return ctrl.New(registry, index).Rename(ctx, logger.Logger, from, to) //nolint:wrapcheck
}
