// Package statefile reads and writes ar2's state where the commands keep it.
//
// The state lives in a container registry rather than in the repository, since it
// changes on every run and would add a commit each time. A local file is the other
// way in, which is what makes a run reproducible while working on one.
//
// It is shared because more than one command has to agree on where the state is: a
// run reads the order and records what it did, and adding a package puts it into that
// order.
package statefile

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aquaproj/ar2/pkg/state"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

// Read reads the state that orders the work.
//
// It comes from the container registry, where 'ar2 init' put it. A path reads a local
// file instead.
//
// A registry that holds none is not a failure: that is what it looks like before
// 'ar2 init' has ever run, and an empty state is enough, since every package is new
// to it and whatever syncs will build the whole thing.
func Read(ctx context.Context, logger *slogutil.Logger, flags *state.Flags, path string) (*state.State, error) {
	if path != "" {
		logger.Info("reading the state from a file", "path", path)
		return readFile(path)
	}
	reg, err := flags.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve the container registry: %w", err)
	}
	logger.Info("pulling the state from the container registry",
		"registry", reg.Registry, "repository", reg.Repository, "tag", state.Tag)
	s, err := state.Fetch(ctx, reg, os.Getenv("GITHUB_TOKEN"))
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("pull the state: %w", err)
	}
	logger.Info("the container registry holds no state; building it from scratch")
	return state.New(), nil
}

// Write stores the state where it was read from.
func Write(ctx context.Context, logger *slogutil.Logger, flags *state.Flags, path string, s *state.State) error {
	if path != "" {
		logger.Info("writing the state to a file", "path", path)
		if err := state.Write(path, s); err != nil {
			return fmt.Errorf("write the state: %w", err)
		}
		return nil
	}
	reg, err := flags.Resolve()
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

func readFile(path string) (*state.State, error) {
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
