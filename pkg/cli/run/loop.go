package run

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	ctrl "github.com/szksh-lab-2/ar2/pkg/controller/run"
	"github.com/szksh-lab-2/ar2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/github"
	"github.com/szksh-lab-2/ar2/pkg/registry"
	"github.com/szksh-lab-2/ar2/pkg/state"
	"github.com/szksh-lab-2/ar2/pkg/verify"
)

// loop works through aqua-registry in the order the state gives, generating up to
// --limit package versions.
func loop(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, httpClient *http.Client, args *Args) error {
	if args.SkipPR && args.OutputDir == "" {
		return errOutputDirRequired
	}
	s, err := readState(ctx, logger, args)
	if err != nil {
		return err
	}

	logger.Info("downloading the aqua-registry definitions", "ref", args.RegistryRef)
	pkgInfos, err := registry.FetchAqua(ctx, gh, args.RegistryRef)
	if err != nil {
		return fmt.Errorf("get the aqua-registry definitions: %w", err)
	}

	c := ctrl.New(gh, generate.New(gh.Repositories), g2.New(gh, args.G2Owner, args.G2Repo),
		github.NewClient(httpClient), verify.New(http.DefaultClient))

	// aqua-registry gains packages continuously, and one the state doesn't know
	// about is never ordered and so never processed. Adding it here means it waits
	// for the next run rather than for the next 'ar2 init'.
	changed, err := c.SyncState(ctx, logger.Logger, s, pkgInfos)
	if err != nil {
		return fmt.Errorf("add the new packages to the state: %w", err)
	}
	if changed {
		if err := writeState(ctx, logger, args, s); err != nil {
			return err
		}
	}

	logger.Info("generating registry.json", "limit", args.Limit, "output_dir", args.OutputDir)
	generated, err := c.Run(ctx, logger.Logger, &ctrl.Input{
		Limit:     args.Limit,
		OutputDir: args.OutputDir,
		SkipPR:    args.SkipPR,
		Verify:    args.Verify,
		State:     s,
		PkgInfos:  pkgInfos,
	})
	if err != nil {
		return fmt.Errorf("generate registry.json: %w", err)
	}
	logger.Info("generated registry.json", "num_of_versions", generated)
	return nil
}

// readState reads the state that orders the work.
//
// It comes from the container registry, where 'ar2 init' put it. --state reads a
// local file instead, which is what makes a run reproducible while working on it.
func readState(ctx context.Context, logger *slogutil.Logger, args *Args) (*state.State, error) {
	if args.StateFile != "" {
		logger.Info("reading the state from a file", "path", args.StateFile)
		return readStateFile(args.StateFile)
	}
	reg, err := args.Flags().Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve the container registry: %w", err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	logger.Info("pulling the state from the container registry",
		"registry", reg.Registry, "repository", reg.Repository, "tag", state.Tag)
	s, err := state.Fetch(ctx, reg, token)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("pull the state: %w", err)
	}
	// Nothing has been pushed yet, which is what a repository looks like before
	// 'ar2 init' has ever run. An empty state is enough: every package aqua-registry
	// has is new to it, so the sync that follows builds the whole thing.
	logger.Info("the container registry holds no state; building it from scratch")
	return state.New(), nil
}

// writeState stores the state where it was read from.
func writeState(ctx context.Context, logger *slogutil.Logger, args *Args, s *state.State) error {
	if args.StateFile != "" {
		logger.Info("writing the state to a file", "path", args.StateFile)
		if err := state.Write(args.StateFile, s); err != nil {
			return fmt.Errorf("write the state: %w", err)
		}
		return nil
	}
	reg, err := args.Flags().Resolve()
	if err != nil {
		return fmt.Errorf("resolve the container registry: %w", err)
	}
	logger.Info("pushing the state to the container registry",
		"registry", reg.Registry, "repository", reg.Repository, "tag", state.Tag)
	if err := state.Store(ctx, reg, os.Getenv("GITHUB_TOKEN"), s); err != nil {
		return fmt.Errorf("push the state: %w", err)
	}
	return nil
}

func readStateFile(path string) (*state.State, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the state file: %w", err)
	}
	defer f.Close()
	s, err := state.Read(f)
	if err != nil {
		return nil, fmt.Errorf("read the state file: %w", err)
	}
	return s, nil
}
