// Package rename implements the 'ar2 rename' command.
package rename

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/identities"
	"github.com/aquaproj/ar2/pkg/cli/token"
	indexctrl "github.com/aquaproj/ar2/pkg/controller/index"
	ctrl "github.com/aquaproj/ar2/pkg/controller/rename"
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

A repository that is renamed or transferred leaves the package under a name nobody uses.

$ ar2 rename sst/opencode anomalyco/opencode

The branch doesn't move, which is what naming it after the package's id is for: nothing
is copied and nothing that wrote the id down is left pointing at nothing. What moves is
the definition on the branch, because the definition is the only thing that says which
package the branch holds -- one still naming the old package would be found under a name
nobody uses, and a run asking for the new name would find no branch and make a second
one. That goes into a pull request, since committing onto a package branch takes one.

The old name becomes an alias of the package, which is how a configuration still asking
for it resolves: aqua reads the table beside the catalogue, which holds both names, and
fetches the package under the name it has now.

The catalogue is brought to the new name straight away, ahead of that pull request. Both
names are the same branch, so a reader resolves either of them to it; what the pull
request settles is which name the registry answers for when a run next asks.

'ar2 run' reports a repository that answers to another name, under "Renamed" in the job
summary. A rename GitHub can't see -- a package that changed names because one
repository started publishing several commands, say -- is this command and nothing else.

Three tokens are read from the environment. GITHUB_TOKEN reads the repository,
AR2_BRANCH_TOKEN is accepted for consistency with a run, and AR2_PR_TOKEN commits the
renamed definition and opens the pull requests.`,
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

	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))
	registry := g2.New(gh, branchGH, prGH, args.G2Owner, args.G2Repo, args.Version)
	if _, _, err := identities.Read(ctx, logger.Logger, registry, httpClient, args.G2Owner, args.G2Repo); err != nil {
		return err //nolint:wrapcheck
	}
	// No auto-merge: a rename is dispatched by a person, and the catalogue's pull
	// request is the last place it can be looked at before the old name stops being
	// listed.
	// No reader of the package branches either: a rename brings one package, and
	// reconciling the whole catalogue isn't what was asked for.
	index := indexctrl.New(registry, nil, args.BaseBranch, nil)
	return ctrl.New(registry, index).Rename(ctx, logger.Logger, from, to) //nolint:wrapcheck
}
