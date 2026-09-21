// Package run implements the 'ar2 run' command.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"github.com/szksh-lab-2/ar2/pkg/cli/flag"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/registry"
	"github.com/szksh-lab-2/ar2/pkg/verify"
)

// Args holds the flag and argument values of the run command.
type Args struct {
	*flag.GlobalFlags

	Target      string // positional argument: <package name>[@<version>]
	SkipPR      bool
	Output      string
	RegistryRef string
	Verify      bool
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
	fs.BoolVar(&args.Verify, "verify", false, "download and extract every asset to check that files[].src matches the archive")
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

	base, err := baseDefinition(ctx, logger, gh, args.RegistryRef, pkgName)
	if err != nil {
		return err
	}

	reg, err := generate.New(gh.Repositories).Generate(ctx, logger.Logger, &generate.Input{
		PkgName: pkgName,
		Version: version,
		Base:    base,
	})
	if err != nil {
		return fmt.Errorf("generate registry.json: %w", err)
	}

	if args.Verify {
		if err := verifyAssets(ctx, logger, version, reg); err != nil {
			return err
		}
	}

	return write(args.Output, reg)
}

// baseDefinition returns aqua-registry's definition of the package.
// It supplies what a release can't express: files, libc variants, and the signing
// configuration. A package aqua-registry doesn't have yet is generated from the
// release alone.
func baseDefinition(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, ref, pkgName string) (*aquaregistry.PackageInfo, error) {
	pkgInfos, err := registry.FetchAqua(ctx, gh, ref)
	if err != nil {
		return nil, fmt.Errorf("get the aqua-registry definitions: %w", err)
	}
	base, ok := pkgInfos[pkgName]
	if !ok {
		logger.Warn("aqua-registry has no definition of the package", "package", pkgName)
	}
	return base, nil
}

// verifyAssets extracts every asset and resolves its files against the archive.
//
// It downloads each asset, so it is off by default: generation alone needs no
// download at all when the release reports digests.
func verifyAssets(ctx context.Context, logger *slogutil.Logger, version string, reg *generate.Registry) error {
	v := verify.New(http.DefaultClient)
	needsReview := false
	for _, asset := range reg.Assets {
		result, err := v.Verify(ctx, logger.Logger, version, asset)
		if err != nil {
			return fmt.Errorf("verify the asset for %s/%s: %w", asset.OS, asset.Arch, err)
		}
		asset.Files = result.Files
		if asset.Checksum == "" {
			asset.Checksum = result.Checksum
			asset.ChecksumAlgorithm = "sha256"
		} else if asset.Checksum != result.Checksum {
			return fmt.Errorf("the digest of %s doesn't match the downloaded asset", asset.Asset)
		}
		if result.NeedsReview {
			needsReview = true
			logger.Warn("the files of this asset don't match the archive",
				"os", asset.OS, "arch", asset.Arch, "unresolved", result.Unresolved)
		}
	}
	if needsReview {
		// The caller creating a pull request has to keep it out of auto-merge.
		logger.Warn("registry.json needs review: files were relocated or couldn't be found")
	}
	return nil
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
