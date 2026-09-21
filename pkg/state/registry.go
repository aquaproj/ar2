package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2/errdef"
)

// Flags are the settings locating the state in a container registry.
// Both 'ar2 init', which writes it, and 'ar2 run', which reads it, take these.
type Flags struct {
	Registry   string
	Repository string
	Username   string
}

// Resolve turns the flags into a Registry, filling in what GitHub Actions provides.
func (f *Flags) Resolve() (*Registry, error) {
	repository := f.Repository
	if repository == "" {
		repository = os.Getenv("GITHUB_REPOSITORY")
	}
	if repository == "" {
		return nil, errRepositoryRequired
	}
	owner, _, found := strings.Cut(repository, "/")
	if !found {
		return nil, errRepositoryFormat
	}
	username := f.Username
	if username == "" {
		username = owner
	}
	return &Registry{
		Registry:   f.Registry,
		Repository: repository,
		Username:   username,
	}, nil
}

// ErrNotFound is returned by Fetch when the container registry holds no state yet,
// which is what a repository looks like before 'ar2 init' has ever run.
var ErrNotFound = errors.New("the container registry holds no state")

// Store writes the state to the container registry.
func Store(ctx context.Context, reg *Registry, token string, s *State) error {
	repo, err := NewRepository(reg, token)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "ar2-state")
	if err != nil {
		return fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := Write(filepath.Join(dir, FileName), s); err != nil {
		return err
	}
	return Push(ctx, repo, dir, Tag)
}

// Fetch pulls the state from the container registry and reads it.
// It returns ErrNotFound when there is none.
func Fetch(ctx context.Context, reg *Registry, token string) (*State, error) {
	repo, err := NewRepository(reg, token)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "ar2-state")
	if err != nil {
		return nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := Pull(ctx, repo, dir, Tag); err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("open the pulled state: %w", err)
	}
	defer f.Close()
	return Read(f)
}
