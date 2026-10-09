// Package validatedefinition implements the 'ar2 validate-definition' command.
package validatedefinition

import (
	"fmt"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	ctrl "github.com/aquaproj/ar2/pkg/controller/validatedefinition"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// New creates the 'ar2 validate-definition' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-definition [<path>]",
		Short: "Check that a package's definition says what the registry reads it for",
		Long: `Check that a package's definition says what the registry reads it for.

registry.yaml is the one file in the registry a person writes, and the only thing that
says which package a branch holds: a branch is named after an id. Nothing on the way in
read it, so a definition that had stopped being YAML passed the branch's checks, merged,
and left the package generating nothing -- a run says so and moves on, which is a line in
a log nobody is watching.

$ ar2 validate-definition
$ ar2 validate-definition registry.yaml

What it asks is what the registry's own readers ask: that it is YAML aqua would read, that
it names a package, and that it says where the package comes from. A branch holding
nothing but its claim to a package is right to say no more -- that is what a branch
created and not yet taken over holds -- so a claim passes.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := logger.SetLevel(gFlags.LogLevel); err != nil {
				return fmt.Errorf("set log level: %w", err)
			}
			path := g2.ConfigFileName
			if len(args) == 1 {
				path = args[0]
			}
			return ctrl.Validate(cmd.OutOrStdout(), path)
		},
	}
	return cmd
}
