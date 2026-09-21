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
	"github.com/szksh-lab-2/ar2/pkg/state"
	"github.com/szksh-lab-2/ar2/pkg/verify"
	"golang.org/x/oauth2"
)

// Args holds the flag and argument values of the run command.
type Args struct {
	*flag.GlobalFlags

	Target      string // positional argument: <package name>[@<version>]
	SkipPR      bool
	Output      string
	RegistryRef string
	Verify      bool
	Limit       int
	OutputDir   string
	StateFile   string
	G2Owner     string
	G2Repo      string
	BaseBranch  string
	Registry    string
	Repository  string
	Username    string
}

// Flags returns the settings locating the state in the container registry.
func (a *Args) Flags() *state.Flags {
	return &state.Flags{
		Registry:   a.Registry,
		Repository: a.Repository,
		Username:   a.Username,
	}
}

// defaultLimit bounds one run. Generating the whole registry in one invocation is
// not possible, so a run takes a slice of the work.
//
// 300 is what saturates the API rate limit without exceeding it. A version costs
// about 3.1 API calls, and a scheduled workflow runs 5 to 6 times an hour, so 300 a
// run is around 5,100 calls an hour against a limit of 5,000. A run takes about nine
// minutes at that size.
const defaultLimit = 300

// New creates the 'ar2 run' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	cmd := &cobra.Command{
		Use:   "run [<package name>[@<version>]]",
		Short: "Generate registry.json from an upstream release",
		Long: `Generate registry.json from an upstream release.

The asset naming rule is not read from a registry. aqua gr infers it from the
release's own asset list, and the result is resolved for every supported
environment the same way aqua resolves it at install time.

Without an argument it works through aqua-registry, most starred package first and
newest version first, skipping what the state says is already generated. The state
records progress, so the next run continues rather than starting over.

$ ar2 run --skip-pr --output-dir out
$ ar2 run cli/cli@v2.101.0 --skip-pr
$ ar2 run cli/cli@v2.101.0 --skip-pr --output registry.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			if len(positional) > 0 {
				args.Target = positional[0]
			}
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.SkipPR, "skip-pr", false, "generate registry.json without creating a branch, a commit, or a pull request")
	fs.StringVar(&args.Output, "output", "", "write registry.json to this file instead of standard output")
	fs.StringVar(&args.RegistryRef, "registry-ref", "main", "the aqua-registry ref the package definition is read from")
	fs.BoolVar(&args.Verify, "verify", true, "download and extract every asset to check that files[].src matches the archive and to read which libc its executables need")
	fs.IntVar(&args.Limit, "limit", defaultLimit, "how many package versions to generate in one run")
	fs.StringVar(&args.OutputDir, "output-dir", "", "write registry.json files under this directory")
	fs.StringVar(&args.StateFile, "state", "", "read the state from this file instead of the container registry")
	fs.StringVar(&args.Registry, "registry", "ghcr.io", "the container registry the state is read from")
	fs.StringVar(&args.Repository, "repository", "", "the container registry repository (default $GITHUB_REPOSITORY)")
	fs.StringVar(&args.Username, "username", "", "the user the GitHub access token belongs to (default the owner of --repository)")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	fs.StringVar(&args.BaseBranch, "base-branch", "main", "the branch the index.json pull request targets")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errTokenRequired
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(token))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}))

	if args.Target == "" {
		return loop(ctx, logger, gh, httpClient, args)
	}
	// One package and version is generated without touching the repository: it is
	// the form used while working on a package, and a pull request for a single
	// version is what the loop makes anyway.
	if !args.SkipPR {
		return errSkipPRRequired
	}
	return single(ctx, logger, gh, args)
}

// single generates registry.json for one package version.
func single(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, args *Args) error {
	pkgName, version, found := strings.Cut(args.Target, "@")
	if !found {
		return errVersionRequired
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

	// Assets without a digest are hashed whether or not --verify is set: a
	// registry.json missing a checksum would defeat the lock file.
	needsReview, err := verify.New(http.DefaultClient).Fill(ctx, logger.Logger, version, reg, args.Verify)
	if err != nil {
		return fmt.Errorf("complete registry.json: %w", err)
	}
	if needsReview {
		// The caller creating a pull request has to keep it out of auto-merge.
		logger.Warn("registry.json needs review: files were relocated or couldn't be found")
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
	if err := json.NewEncoder(out).Encode(reg); err != nil {
		return fmt.Errorf("write registry.json: %w", err)
	}
	return nil
}
