// Package test implements the 'ar2 test' command.
package test

import (
	"context"
	"fmt"
	"net/http"
	"os"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/cli/flag"
	ctrl "github.com/aquaproj/ar2/pkg/controller/test"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/verify"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"go.yaml.in/yaml/v3"
)

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	Definition   string
	Package      string
	OS           string
	Arch         string
	Environments bool
	Signatures   bool
}

// New creates the 'ar2 test' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	cmd := &cobra.Command{
		Use:   "test <registry-1.json>...",
		Short: "Check generated registry.json files against the releases they describe",
		Long: `Check generated registry.json files against the releases they describe.

This is the CI of a package branch. A pull request adding a version is merged without
a human reading it, so the trust in what was generated comes from here: every asset is
downloaded, hashed against the checksum the entry carries, opened to see that the files
it names are where it says, and checked against the signatures it claims.

$ ar2 test versions/v1.5.6/registry-1.json

The version comes from the path, which is versions/<version>/registry-1.json on a package
branch. The package name comes from registry.yaml beside it, and --package overrides it
for a file checked from somewhere else.

--os and --arch limit the run to the entries of one environment, which is how a job
running on a machine of that environment checks the entry meant for it.

$ ar2 test --os windows --arch amd64 versions/v1.5.6/registry-1.json

--environments lists the environments the files describe instead of checking them, so
that the jobs can be worked out from the files rather than fixed in advance.

An asset in a format this machine can't open is reported and passed over rather than
failed: a dmg on Linux is a package this machine can't look inside.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, paths []string) error {
			if args.Environments {
				return ctrl.Environments(cmd.OutOrStdout(), paths)
			}
			return action(cmd.Context(), logger, args, paths)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.Definition, "definition", g2.ConfigFileName, "the definition to read the package name from")
	fs.StringVar(&args.Package, "package", "", "the package name, instead of reading it from the definition")
	fs.StringVar(&args.OS, "os", "", "check only the entries of this operating system")
	fs.StringVar(&args.Arch, "arch", "", "check only the entries of this architecture")
	fs.BoolVar(&args.Environments, "environments", false, "list the environments the files describe instead of checking them")
	fs.BoolVar(&args.Signatures, "signatures", true, "check the signatures the entries claim")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, paths []string) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}

	pkgName := args.Package
	if pkgName == "" {
		name, err := packageName(args.Definition)
		if err != nil {
			return err
		}
		pkgName = name
	}
	logger.Logger = logger.With("package_name", pkgName)

	verifier, err := verifier(ctx, logger, args.Signatures)
	if err != nil {
		return err
	}
	env := ctrl.Environment{OS: args.OS, Arch: args.Arch}
	return ctrl.New(verifier, env).Run(ctx, logger.Logger, pkgName, paths) //nolint:wrapcheck
}

// packageName reads the name out of the definition at the root of the package branch.
//
// The name is needed to verify an attestation, which names the repository that built
// the asset, and it is what the log lines are read by.
func packageName(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the package definition: %w", err)
	}
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return "", fmt.Errorf("read the package definition as YAML: %w", err)
	}
	if cfg.PackageInfo == nil || cfg.GetName() == "" {
		return "", errNoName
	}
	return cfg.GetName(), nil
}

// verifier builds what downloads and opens an asset.
func verifier(ctx context.Context, logger *slogutil.Logger, signatures bool) (*verify.Verifier, error) {
	if !signatures {
		return verify.New(http.DefaultClient, nil), nil
	}
	s, err := sign.New(ctx, logger.Logger, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("prepare the signature verification: %w", err)
	}
	return verify.New(http.DefaultClient, s), nil
}
