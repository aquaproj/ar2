// Package regenerate implements the 'ar2 regenerate' command.
package regenerate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/run"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/registry"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

// errTokenRequired is returned when no access token is available.
var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")

// Args holds the command's flags.
type Args struct {
	*flag.GlobalFlags
	RegistryRef string
	Verify      bool
	DryRun      bool
	G2Owner     string
	G2Repo      string
}

// New creates the 'ar2 regenerate' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "regenerate <package name> [<version>...]",
		Short: "Generate again what the registry already holds",
		Long: `Generate again what the registry already holds.

What the registry serves is frozen per version. A definition that turns out wrong is
wrong in every file generated under it, and 'ar2 run' won't touch them: it generates
the versions the registry is missing. So fixing the definition is half of it, and
this is the other half.

$ ar2 regenerate astral-sh/uv 0.12.19 0.12.18
$ ar2 regenerate astral-sh/uv --dry-run

Named versions are generated again; naming none does every version the registry
holds, which for a package with a long history is a large pull request. A version the
registry doesn't hold is refused rather than added, because adding one is a run's job.

The definition comes from the package's branch and nowhere else. aqua-registry's
converted definition is what a run writes the first time it reaches a package, and
generating from it here would produce files the registry's own definition doesn't.

Only the versions that come out differently are committed. A package nothing has
changed under therefore comes to nothing, which is what makes it safe to point this
at a whole package to find out whether anything moved.

Auto-merge is never turned on. CI can say the new file describes the release it says
it does; it can't say that replacing the old one was right. The old file stays in the
branch's history either way.

A package with an open pull request is refused. The commit is written against the
package branch and the head branch is pointed at it, so an open pull request's
commits would be discarded.

Three tokens are read from the environment. GITHUB_TOKEN reads the repositories,
AR2_BRANCH_TOKEN is unused here beyond being accepted, and AR2_PR_TOKEN commits and
opens the pull request.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, as []string) error {
			return action(cmd.Context(), logger, args, as[0], as[1:])
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.RegistryRef, "registry-ref", "main", "the aqua-registry ref the package definition is read from")
	fs.BoolVar(&args.Verify, "verify", true, "download and extract every asset to check that files[].src matches the archive and to read which libc its executables need")
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which versions would change without committing anything")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args, pkgName string, versions []string) error {
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
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ghToken}))

	c, err := controller(ctx, logger, gh, httpClient, args)
	if err != nil {
		return err
	}
	base, err := baseDefinition(ctx, logger, gh, args.RegistryRef, pkgName)
	if err != nil {
		return err
	}

	changed, err := c.Regenerate(ctx, logger.Logger, &ctrl.RegenerateInput{
		PkgName:     pkgName,
		Versions:    versions,
		Verify:      args.Verify,
		Base:        base,
		RegistryRef: args.RegistryRef,
		DryRun:      args.DryRun,
	})
	if err != nil {
		return fmt.Errorf("generate registry.json again: %w", err)
	}
	logger.Info("generated registry.json again", "num_of_versions", changed)
	return nil
}

// controller assembles what a regeneration works with.
//
// It is the loop's controller: the pipeline that generates a version is the same one,
// and a repair that generated differently from a run would be repairing towards
// something the registry doesn't produce. What it isn't given is the state and the
// sweep, which are about which package to work on next and have no part in this.
func controller(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, httpClient *http.Client, args *Args) (*ctrl.Controller, error) {
	branchGH, err := token.Client(token.BranchEnv)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names the token it is for
	}
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	v, err := verifier(ctx, logger, httpClient, args)
	if err != nil {
		return nil, err
	}
	// No renamer: a regeneration is for one package under the name it has, and
	// noticing that the name has changed is the sweep's job.
	return ctrl.New(gh, generate.New(gh.Repositories),
		g2.New(gh, branchGH, prGH, args.G2Owner, args.G2Repo),
		github.NewClient(httpClient), v, nil), nil
}

// verifier builds what downloads an asset and decides whether the entry for it can
// be merged.
func verifier(ctx context.Context, logger *slogutil.Logger, httpClient *http.Client, args *Args) (*verify.Verifier, error) {
	if !args.Verify {
		return verify.New(http.DefaultClient, nil), nil
	}
	signatures, err := sign.New(ctx, logger.Logger, httpClient)
	if err != nil {
		return nil, fmt.Errorf("prepare the signature verification: %w", err)
	}
	return verify.New(http.DefaultClient, signatures), nil
}

// baseDefinition returns aqua-registry's definition of the package, which supplies
// what a release can't express: files, libc variants, and the signing configuration.
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
