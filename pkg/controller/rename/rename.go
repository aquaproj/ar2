// Package rename moves a package to the name its repository has now.
//
// A package's name is part of everything: the branch its generated versions live on, the
// path aqua fetches them from, the entry the catalogue lists it under. A repository that
// is renamed or transferred leaves all of it under a name nobody uses, and the versions
// are not something to generate again -- each one was downloaded, hashed and opened on
// six machines to get there.
//
// So the branch is carried over and the old name becomes an alias, which is how a
// configuration still asking for it resolves.
package rename

import (
	"context"
	"fmt"
	"log/slog"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// Registry is what a rename reads and writes.
type Registry interface {
	RenamePackage(ctx context.Context, from, to string) (bool, error)
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
}

// Catalogue is the index a renamed package has to be listed in under its new name.
type Catalogue interface {
	Rename(ctx context.Context, logger *slog.Logger, from, to string, cfg *aquag2.Config) error
}

// Controller renames a package.
type Controller struct {
	g2    Registry
	index Catalogue
}

// New creates a Controller.
func New(registry Registry, index Catalogue) *Controller {
	return &Controller{g2: registry, index: index}
}

// Rename moves the package from one name to the other.
//
// The branch first, then the catalogue. A branch under the new name that the catalogue
// doesn't list yet is what the reconciliation exists to find; a catalogue naming a branch
// that isn't there is a registry that answers nothing for a package it says it has.
func (c *Controller) Rename(ctx context.Context, logger *slog.Logger, from, to string) error {
	if from == to {
		return fmt.Errorf("%w: %s", errSameName, from)
	}
	logger = logger.With("package", from, "renamed_to", to)

	moved, err := c.g2.RenamePackage(ctx, from, to)
	if err != nil {
		return fmt.Errorf("move the package's branch: %w", err)
	}
	if moved {
		logger.Info("carried the package's branch over to its new name")
	} else {
		// The branch is already there, from a run that stopped before the catalogue.
		// Saying so is the difference between a rename that had nothing to do and one
		// that was half done.
		logger.Info("the new name already has a branch; bringing the catalogue up to it")
	}

	cfg, err := c.g2.Config(ctx, to)
	if err != nil {
		return fmt.Errorf("get the definition of the renamed package: %w", err)
	}
	if err := c.index.Rename(ctx, logger, from, to, cfg); err != nil {
		return fmt.Errorf("bring the catalogue to the new name: %w", err)
	}
	return nil
}
