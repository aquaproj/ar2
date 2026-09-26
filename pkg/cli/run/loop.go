package run

import (
	"context"
	"fmt"
	"net/http"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/cli/statefile"
	"github.com/aquaproj/ar2/pkg/cli/token"
	indexctrl "github.com/aquaproj/ar2/pkg/controller/index"
	renamectrl "github.com/aquaproj/ar2/pkg/controller/rename"
	ctrl "github.com/aquaproj/ar2/pkg/controller/run"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/registry"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// loop works through aqua-registry in the order the state gives, generating up to
// --limit package versions.
func loop(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, httpClient *http.Client, args *Args) error {
	if args.SkipPR && args.OutputDir == "" {
		return errOutputDirRequired
	}
	s, err := statefile.Read(ctx, logger, args.Flags(), args.StateFile)
	if err != nil {
		return err //nolint:wrapcheck // the error already names what it failed to read
	}

	logger.Info("downloading the aqua-registry definitions", "ref", args.RegistryRef)
	pkgInfos, err := registry.FetchAqua(ctx, gh, args.RegistryRef)
	if err != nil {
		return fmt.Errorf("get the aqua-registry definitions: %w", err)
	}

	c, err := controller(ctx, logger, gh, httpClient, args)
	if err != nil {
		return err
	}

	if err := syncState(ctx, logger, c, args, s, pkgInfos); err != nil {
		return err
	}

	logger.Info("generating registry.json", "limit", args.Limit, "output_dir", args.OutputDir)
	generated, err := c.Run(ctx, logger.Logger, &ctrl.Input{
		Limit:       args.Limit,
		OutputDir:   args.OutputDir,
		SkipPR:      args.SkipPR,
		Verify:      args.Verify,
		State:       s,
		PkgInfos:    pkgInfos,
		RegistryRef: args.RegistryRef,
		BaseBranch:  args.BaseBranch,
	})
	if err != nil {
		return fmt.Errorf("generate registry.json: %w", err)
	}
	logger.Info("generated registry.json", "num_of_versions", generated)

	// The run wrote down what each package turned out to hold. Without this the
	// next run would sweep the registry and find every package looking as though it
	// had never been looked at.
	if !args.SkipPR {
		if err := statefile.Write(ctx, logger, args.Flags(), args.StateFile, s); err != nil {
			return err //nolint:wrapcheck // the error already names what it failed to write
		}
	}
	return nil
}

// controller assembles what a run works with.
func controller(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, httpClient *http.Client, args *Args) (*ctrl.Controller, error) {
	reg, err := registryClient(gh, args)
	if err != nil {
		return nil, err
	}
	v, err := verifier(ctx, logger, httpClient, args)
	if err != nil {
		return nil, err
	}
	// A run that writes nothing doesn't move a package either: the branch would be
	// carried over while the files that go with it were only printed.
	var renamer ctrl.Renamer
	if !args.SkipPR {
		renamer = renamectrl.New(reg, indexctrl.New(reg, nil, args.BaseBranch, nil))
	}
	return ctrl.New(gh, generate.New(gh.Repositories), reg,
		github.NewClient(httpClient), v, renamer), nil
}

// verifier builds what downloads an asset and decides whether the entry for it can
// be merged.
//
// Signatures are checked only when the archives are: both need the asset on disk,
// and a run that skips the download has nothing to check either against.
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

// syncState adds the packages aqua-registry has gained and stores the result.
//
// aqua-registry gains packages continuously, and one the state doesn't know about is
// never ordered and so never processed. Adding it here means it waits for the next
// run rather than for the next 'ar2 init'. It is stored before the run rather than
// after it, so that a run which fails partway doesn't have to find them again.
func syncState(ctx context.Context, logger *slogutil.Logger, c *ctrl.Controller, args *Args, s *state.State, pkgInfos map[string]*aquaregistry.PackageInfo) error {
	changed, err := c.SyncState(ctx, logger.Logger, s, pkgInfos)
	if err != nil {
		return fmt.Errorf("add the new packages to the state: %w", err)
	}
	if !changed {
		return nil
	}
	return statefile.Write(ctx, logger, args.Flags(), args.StateFile, s) //nolint:wrapcheck
}

// registryClient builds the client that reads and writes aqua-registry-g2.
//
// Three tokens go into it, because two of the things a run does are things the
// repository's own token can't: create a package branch past the ruleset, and open a
// pull request whose checks start without a person approving them.
func registryClient(gh *gogithub.Client, args *Args) (*g2.Client, error) {
	branchGH, err := token.Client(token.BranchEnv)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names the token it is for
	}
	prGH, err := token.Client(token.PREnv)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names the token it is for
	}
	return g2.New(gh, branchGH, prGH, args.G2Owner, args.G2Repo), nil
}
