// Package run implements the 'ar2 run' command.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gogithub "github.com/google/go-github/v91/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"github.com/szksh-lab-2/ar2/pkg/cli/flag"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/registry"
)

// Args holds the flag and argument values of the run command.
type Args struct {
	*flag.GlobalFlags

	Target      string // positional argument: <package name>[@<version>]
	SkipPR      bool
	Output      string
	RegistryRef string
}

// New creates the 'ar2 run' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	cmd := &cobra.Command{
		Use:   "run <package name>[@<version>]",
		Short: "Generate registry.json from an upstream release",
		Long: `Generate registry.json from an upstream release.

The asset naming rule is not read from a registry. aqua gr infers it from the
release's own asset list, and the result is resolved for every supported
environment the same way aqua resolves it at install time.

$ ar2 run cli/cli@v2.101.0 --skip-pr
$ ar2 run cli/cli@v2.101.0 --skip-pr --output registry.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			args.Target = positional[0]
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.SkipPR, "skip-pr", false, "generate registry.json without creating a branch, a commit, or a pull request")
	fs.StringVar(&args.Output, "output", "", "write registry.json to this file instead of standard output")
	fs.StringVar(&args.RegistryRef, "registry-ref", "main", "the aqua-registry ref the package definition is read from")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	if !args.SkipPR {
		// Creating the branch, the commit, and the pull request isn't implemented yet.
		return errSkipPRRequired
	}

	pkgName, version, found := strings.Cut(args.Target, "@")
	if !found {
		return errVersionRequired
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errTokenRequired
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(token))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}

	// aqua-registry's definition supplies what a release can't express: files, libc
	// variants, and the signing configuration. A package aqua-registry doesn't have
	// yet is generated from the release alone.
	pkgInfos, err := registry.FetchAqua(ctx, gh, args.RegistryRef)
	if err != nil {
		return fmt.Errorf("get the aqua-registry definitions: %w", err)
	}
	base, ok := pkgInfos[pkgName]
	if !ok {
		logger.Warn("aqua-registry has no definition of the package", "package", pkgName)
	}

	reg, err := generate.New(gh.Repositories).Generate(ctx, logger.Logger, &generate.Input{
		PkgName: pkgName,
		Version: version,
		Base:    base,
	})
	if err != nil {
		return fmt.Errorf("generate registry.json: %w", err)
	}

	return write(args.Output, reg)
}

// write writes registry.json to path, or to standard output when path is empty.
func write(path string, reg *generate.Registry) error {
	out := os.Stdout
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create the output file: %w", err)
		}
		defer f.Close()
		out = f
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(reg); err != nil {
		return fmt.Errorf("write registry.json: %w", err)
	}
	return nil
}
