// Package validateindex implements the 'ar2 validate-index' command.
package validateindex

import (
	"fmt"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	ctrl "github.com/aquaproj/ar2/pkg/controller/validateindex"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// New creates the 'ar2 validate-index' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-index [<path>]",
		Short: "Check that index.json says what aqua reads it for",
		Long: `Check that index.json says what aqua reads it for.

The catalogue is one file holding the name and description of every package the
registry has: choosing a package means reading all of them before knowing which one is
wanted, so it can't be one request per package. It is written by ar2 and merged
without a reader, which is why this is a check rather than a convention.

$ ar2 validate-index

It is read as the type aqua reads, so a field aqua wouldn't read is an error rather
than something ignored. Beyond that: there is a packages list, every entry has a name,
and no name appears twice.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := logger.SetLevel(gFlags.LogLevel); err != nil {
				return fmt.Errorf("set log level: %w", err)
			}
			path := g2.IndexFileName
			if len(args) == 1 {
				path = args[0]
			}
			return ctrl.Validate(cmd.OutOrStdout(), path)
		},
	}
	return cmd
}
