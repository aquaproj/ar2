package state

import (
	"context"
	"fmt"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// Registry describes where the state artifact lives.
type Registry struct {
	// Registry is the container registry host, e.g. "ghcr.io".
	Registry string
	// Repository is the OCI repository, e.g. "aquaproj/ar2".
	Repository string
	// Username is the user the token belongs to.
	Username string
}

const (
	mediaType    = "application/vnd.ar2.state.v1+json"
	artifactType = "application/vnd.ar2.state.v1"
)

// NewRepository creates a client for the remote OCI repository.
func NewRepository(reg *Registry, token string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reg.Registry + "/" + reg.Repository)
	if err != nil {
		return nil, fmt.Errorf("create a client for a remote repository: %w", err)
	}
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.DefaultCache,
		Credential: auth.StaticCredential(reg.Registry, auth.Credential{
			Username: reg.Username,
			Password: token,
		}),
	}
	return repo, nil
}

// Push uploads the state file in dir to the remote repository under tag.
func Push(ctx context.Context, repo *remote.Repository, dir, tag string) error {
	fs, err := file.New(dir)
	if err != nil {
		return fmt.Errorf("create a file store: %w", err)
	}
	defer fs.Close()

	descriptor, err := fs.Add(ctx, FileName, mediaType, "")
	if err != nil {
		return fmt.Errorf("add the state file to the file store: %w", err)
	}

	manifest, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, artifactType, oras.PackManifestOptions{
		Layers: []v1.Descriptor{descriptor},
	})
	if err != nil {
		return fmt.Errorf("pack the state file: %w", err)
	}

	if err := fs.Tag(ctx, manifest, tag); err != nil {
		return fmt.Errorf("tag the packed manifest: %w", err)
	}

	if _, err := oras.Copy(ctx, fs, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("push the state to the container registry: %w", err)
	}
	return nil
}

// Pull downloads the state file at tag into dir.
func Pull(ctx context.Context, repo *remote.Repository, dir, tag string) error {
	fs, err := file.New(dir)
	if err != nil {
		return fmt.Errorf("create a file store: %w", err)
	}
	defer fs.Close()

	if _, err := oras.Copy(ctx, repo, tag, fs, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("pull the state from the container registry: %w", err)
	}
	return nil
}
