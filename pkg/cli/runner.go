// Package cli provides the command-line interface layer for ar2.
// It parses commands and flags with cobra and delegates the work to the
// controller packages.
package cli

import (
	"context"

	"github.com/aquaproj/ar2/pkg/cli/add"
	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/index"
	"github.com/aquaproj/ar2/pkg/cli/initcmd"
	"github.com/aquaproj/ar2/pkg/cli/regenerate"
	"github.com/aquaproj/ar2/pkg/cli/remove"
	"github.com/aquaproj/ar2/pkg/cli/rename"
	"github.com/aquaproj/ar2/pkg/cli/run"
	"github.com/aquaproj/ar2/pkg/cli/state"
	"github.com/aquaproj/ar2/pkg/cli/test"
	"github.com/aquaproj/ar2/pkg/cli/validateindex"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// Run creates and executes the ar2 CLI application.
func Run(ctx context.Context, logger *slogutil.Logger, env *cobrautil.Env) error {
	gFlags := &flag.GlobalFlags{}
	// The context reaches the commands through ExecuteContext below, which is what
	// cobra hands to the action as cmd.Context(); building the tree needs none.
	cmd := newCommand(logger, env, gFlags) //nolint:contextcheck
	return cmd.ExecuteContext(ctx)         //nolint:wrapcheck
}

// newCommand builds the command tree. It is separate from Run so that a test can run
// the real tree with its output captured.
func newCommand(logger *slogutil.Logger, env *cobrautil.Env, gFlags *flag.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ar2",
		Short: "Maintain aqua-registry-g2. https://github.com/aquaproj/ar2",
		Long: `Maintain aqua-registry-g2.

ar2 generates the statically resolved registry.json of aqua-registry-g2 from upstream
releases, and opens pull requests with the result.

See each subcommand's help with 'ar2 help <command>'.`,
	}
	flag.LogLevel(cmd.PersistentFlags(), &gFlags.LogLevel)
	cmd.AddCommand(
		initcmd.New(logger, gFlags),
		run.New(logger, gFlags),
		add.New(logger, gFlags),
		rename.New(logger, gFlags),
		regenerate.New(logger, gFlags),
		remove.New(logger, gFlags),
		index.New(logger, gFlags),
		state.New(logger, gFlags),
		test.New(logger, gFlags),
		validateindex.New(logger, gFlags),
	)
	return cobrautil.Command(env, cmd, nil)
}
