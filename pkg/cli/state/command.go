// Package state implements the 'ar2 state' command.
package state

import (
	"context"
	"fmt"
	"io"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/statefile"
	ctrl "github.com/aquaproj/ar2/pkg/controller/state"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	StateFile  string
	Registry   string
	Repository string
	Username   string
}

// Flags returns the settings locating the state in the container registry.
func (a *Args) Flags() *state.Flags {
	return &state.Flags{
		Registry:   a.Registry,
		Repository: a.Repository,
		Username:   a.Username,
	}
}

// New creates the 'ar2 state' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "state [<package name>...]",
		Short: "Say what the state says about the registry's progress",
		Long: `Say what the state says about the registry's progress.

The state decides which package a run works on next, and nothing else can be asked about
it: the repository says what the registry holds, not whose turn it is.

Without an argument it says how far the whole registry has got.

$ ar2 state

Named packages are answered for one at a time, which is what "why hasn't this package
been generated" needs: how many turns it has had, whether it is behind the rest of the
order, whether it already holds every version the last sweep saw, and whether its history
has ever been walked.

$ ar2 state cli/cli suzuki-shunsuke/ghalint

It only reads, so unlike everything else here it runs from a laptop: GITHUB_TOKEN with
access to the container registry is enough. --state reads a local file instead.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, names []string) error {
			return action(cmd.Context(), logger, args, cmd.OutOrStdout(), names)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.StateFile, "state", "", "read the state from this file instead of the container registry")
	fs.StringVar(&args.Registry, "registry", "ghcr.io", "the container registry the state is read from")
	fs.StringVar(&args.Repository, "repository", "", "the container registry repository (default $GITHUB_REPOSITORY)")
	fs.StringVar(&args.Username, "username", "", "the user the GitHub access token belongs to (default the owner of --repository)")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, out io.Writer, names []string) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	s, err := statefile.Read(ctx, logger, args.Flags(), args.StateFile)
	if err != nil {
		return err //nolint:wrapcheck // the error already names what it failed to read
	}
	if len(names) == 0 {
		return ctrl.Summary(out, s) //nolint:wrapcheck
	}
	return ctrl.Packages(out, s, names) //nolint:wrapcheck
}
