package run

import (
	"context"
	"fmt"
	"os"

	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	ctrl "github.com/szksh-lab-2/ar2/pkg/controller/run"
	"github.com/szksh-lab-2/ar2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/generate"
	"github.com/szksh-lab-2/ar2/pkg/registry"
	"github.com/szksh-lab-2/ar2/pkg/state"
)

// loop works through aqua-registry in the order the state gives, generating up to
// --limit package versions.
func loop(ctx context.Context, logger *slogutil.Logger, gh *gogithub.Client, args *Args) error {
	if args.OutputDir == "" {
		return errOutputDirRequired
	}
	s, err := readState(args.StateFile)
	if err != nil {
		return err
	}

	logger.Info("downloading the aqua-registry definitions", "ref", args.RegistryRef)
	pkgInfos, err := registry.FetchAqua(ctx, gh, args.RegistryRef)
	if err != nil {
		return fmt.Errorf("get the aqua-registry definitions: %w", err)
	}

	logger.Info("generating registry.json", "limit", args.Limit, "output_dir", args.OutputDir)
	generated, err := ctrl.New(gh, generate.New(gh.Repositories), g2.New(gh, args.G2Owner, args.G2Repo)).
		Run(ctx, logger.Logger, &ctrl.Input{
			Limit:     args.Limit,
			OutputDir: args.OutputDir,
			State:     s,
			PkgInfos:  pkgInfos,
		})
	if err != nil {
		return fmt.Errorf("generate registry.json: %w", err)
	}
	logger.Info("generated registry.json", "num_of_versions", generated)
	return nil
}

// readState reads the state from a file.
//
// The state carries the star counts that order the work. Reading it from the
// container registry isn't wired up yet, so a path is required.
func readState(path string) (*state.State, error) {
	if path == "" {
		return nil, errStateRequired
	}
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
