// Package validatetemplates implements the 'ar2 validate-templates' command.
package validatetemplates

import (
	"fmt"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	ctrl "github.com/aquaproj/ar2/pkg/controller/validatetemplates"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// New creates the 'ar2 validate-templates' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate-templates <path>...",
		Short: "Check that template-1.json files are what ar2 renders from their definitions",
		Long: `Check that template-1.json files are what ar2 renders from their definitions.

A package served as a template has template-1.json beside its registry.yaml, rendered
from it by 'ar2 index' and committed in the catalogue's pull request. aqua reads it as it
is, so what a pull request brings has to be exactly that rendering.

$ ar2 validate-templates pkgs/01/1795000001/template-1.json

Run it in the pull request merged into its base. A path that exists must be what the
registry.yaml beside it renders, for a package served as a template that ar2.yaml doesn't
ignore. A path that doesn't exist was removed, which is right only for a package that
isn't served as a template, or is ignored.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := logger.SetLevel(gFlags.LogLevel); err != nil {
				return fmt.Errorf("set log level: %w", err)
			}
			return ctrl.Validate(cmd.OutOrStdout(), g2.RegistryConfigFileName, args)
		},
	}
}
