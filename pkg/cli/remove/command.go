// Package remove implements the 'ar2 remove' command.
package remove

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/remove"
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
	Reason     string
	BaseBranch string
	G2Owner    string
	G2Repo     string
}

// New creates the 'ar2 remove' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "remove <package name>",
		Short: "Stop the registry serving a package",
		Long: `Stop the registry serving a package.

Not something the registry does as a rule. What it publishes for a version is meant to
stay what it was, and somebody's configuration may name the package. The exceptions are
a package aqua can't install whatever is generated for it, and malware.

$ ar2 remove foo/bar --reason "It is malware: <what was found, and where it was said>"

It is three things at once, and this does all of them: the registry stops generating the
package, stops listing it, and stops holding what it generated. Any one alone leaves a
state nobody meant -- an entry for a package that can't be fetched, or files nothing
lists that the next run adds to.

It opens two pull requests, because they are on different branches. The first carries
the registry's configuration and the catalogue; the second takes the generated files off
the package's branch. Merge the first one first: a package still in the order has its
files generated again by the next run. Neither is set to auto-merge.

A reason is required. It goes into ignored_packages, which is what the next person to
wonder why the package isn't here reads.

What this cannot reach is a lock file. One that already holds the package carries the URL
and the checksum of every file it needs, and nothing here takes that away. Where that
matters, saying so where people will read it is the part that reaches them.

Three tokens are read from the environment: GITHUB_TOKEN reads the repository,
AR2_PR_TOKEN commits and opens the pull requests, and AR2_BRANCH_TOKEN is not used.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, as []string) error {
			return action(cmd.Context(), logger, args, as[0])
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.Reason, "reason", "", "why the registry stops serving the package")
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the first pull request targets")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, pkgName string) error {
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

	// No branch token: a package being removed is one the registry holds, so every
	// branch this touches is already there.
	registry := g2.New(gh, nil, prGH, args.G2Owner, args.G2Repo)
	return ctrl.New(registry, args.BaseBranch).Remove(ctx, logger.Logger, &ctrl.Input{ //nolint:wrapcheck
		PkgName: pkgName,
		Reason:  args.Reason,
	})
}
