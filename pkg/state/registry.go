package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Fetch pulls the state from the container registry and reads it.
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
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("open the pulled state: %w", err)
	}
	defer f.Close()
	return Read(f)
}
