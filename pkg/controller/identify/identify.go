// Package identify puts every package the registry holds on a branch named after its id.
//
// A branch was named after the package, which made the name the address of everything:
// the branch, the path aqua fetches a version from, the entry the catalogue lists. A
// package whose repository is renamed then has to be moved, and everything that already
// wrote the old name down is wrong. An id doesn't change when a name does, so the name
// becomes something the catalogue maps and nothing else depends on.
//
// This runs once. It creates a branch per package and creates nothing else, so a second
// run sees the branches it made and has nothing to do.
package identify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
)

// Registry is the part of aqua-registry-g2 this reads and writes.
type Registry interface {
	PlanIdentity(ctx context.Context, pkgName, id string) (*g2.Identification, error)
	Identify(ctx context.Context, pkgName string, plan *g2.Identification) error
}

// Controller identifies the packages of a registry.
type Controller struct {
	registry Registry
	// ids is what each package's branch is named after. It says which packages have
	// already been carried over, and it is where the id of one that hasn't is minted:
	// minting steps past every id a branch already has.
	ids *g2.Identities
	// defs is the definition on every package branch, keyed by branch. The branches still
	// named after a package are the ones to carry over, and their definitions are here
	// because the table was read out of the same pass.
	defs map[string]string
}

// New creates a Controller.
func New(registry Registry, ids *g2.Identities, defs map[string]string) *Controller {
	return &Controller{registry: registry, ids: ids, defs: defs}
}

// Identify carries every branch still named after a package over to one named after an id.
//
// The branches are the list of what to do, because a branch named after a package is what
// says which package it holds -- that name is the only place it is written down. A branch
// holding no definition isn't one of them: it holds nothing but the template every new
// branch starts with, and a run taking the package over again makes it afresh.
//
// A package that fails doesn't stop the rest. What is created is created, and a second run
// picks up where this one got to, so the useful thing to report is everything that went
// wrong rather than the first of it.
func (c *Controller) Identify(ctx context.Context, logger *slog.Logger, w io.Writer, dryRun bool) error {
	var (
		errs  []error
		done  int
		ready int
	)
	// In branch order, so that two runs of this read the same way.
	for _, branch := range slices.Sorted(maps.Keys(c.defs)) {
		if _, ok := g2.BranchID(branch); ok {
			// Already named after an id, which is what this creates.
			continue
		}
		pkgName, ok := aquag2.PackageName(branch)
		if !ok {
			// main, and anything else that isn't a package's branch.
			continue
		}
		if id, ok := c.ids.ID(pkgName); ok {
			logger.Debug("the package is already on the branch named after its id",
				"package_name", pkgName, "id", id)
			done++
			continue
		}
		// The id its branch will be named after. Minting steps past every id a branch
		// already has, so two packages carried over in the same second don't collide.
		id := c.ids.Mint(pkgName)
		plan, err := c.registry.PlanIdentity(ctx, pkgName, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("plan the identity of %s: %w", pkgName, err))
			continue
		}
		if plan == nil {
			done++
			continue
		}
		report(w, pkgName, plan)
		ready++
		if dryRun {
			continue
		}
		if err := c.registry.Identify(ctx, pkgName, plan); err != nil {
			errs = append(errs, fmt.Errorf("identify %s: %w", pkgName, err))
			continue
		}
		logger.Info("created the branch named after the package's id",
			"package_name", pkgName, "branch", plan.Branch)
	}
	summarize(w, done, ready, len(errs), dryRun)
	return errors.Join(errs...)
}

// report says what one package's branch would hold that its old one doesn't.
func report(w io.Writer, pkgName string, plan *g2.Identification) {
	fmt.Fprintf(w, "%s\t%s\n", pkgName, plan.Branch)
	if plan.Config != "" {
		fmt.Fprintf(w, "\tthe definition names the package\n")
	}
	for _, moved := range plan.Moved {
		fmt.Fprintf(w, "\t%s -> %s\n", moved.From, moved.To)
	}
}

func summarize(w io.Writer, done, ready, failed int, dryRun bool) {
	verb := "created"
	if dryRun {
		verb = "to create"
	}
	fmt.Fprintf(w, "\n%d %s, %d already there, %d failed\n", ready, verb, done, failed)
}
