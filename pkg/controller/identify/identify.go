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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

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
}

// New creates a Controller.
func New(registry Registry) *Controller {
	return &Controller{registry: registry}
}

// Identify gives every package the catalogue lists a branch named after its id.
//
// The catalogue is where the ids are, so it is also the list of what to do: a branch the
// catalogue doesn't name is a branch nothing can reach, and giving it a second name
// wouldn't change that.
//
// A package that fails doesn't stop the rest. What is created is created, and a second run
// picks up where this one got to, so the useful thing to report is everything that went
// wrong rather than the first of it.
func (c *Controller) Identify(ctx context.Context, logger *slog.Logger, w io.Writer, path string, dryRun bool) error {
	index, err := readIndex(path)
	if err != nil {
		return err
	}
	var (
		errs  []error
		done  int
		ready int
	)
	for _, pkg := range index.Packages {
		if pkg == nil {
			continue
		}
		plan, err := c.registry.PlanIdentity(ctx, pkg.Name, pkg.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("plan the identity of %s: %w", pkg.Name, err))
			continue
		}
		if plan == nil {
			logger.Debug("the package is already on the branch named after its id",
				"package_name", pkg.Name, "id", pkg.ID)
			done++
			continue
		}
		report(w, pkg, plan)
		ready++
		if dryRun {
			continue
		}
		if err := c.registry.Identify(ctx, pkg.Name, plan); err != nil {
			errs = append(errs, fmt.Errorf("identify %s: %w", pkg.Name, err))
			continue
		}
		logger.Info("created the branch named after the package's id",
			"package_name", pkg.Name, "branch", plan.Branch)
	}
	summarize(w, done, ready, len(errs), dryRun)
	return errors.Join(errs...)
}

// report says what one package's branch would hold that its old one doesn't.
func report(w io.Writer, pkg *aquag2.IndexPackage, plan *g2.Identification) {
	fmt.Fprintf(w, "%s\t%s\n", pkg.Name, plan.Branch)
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

// readIndex reads the catalogue from the checkout rather than from the registry, because
// the ids it hands out are the ones that were merged into it.
func readIndex(path string) (*aquag2.Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the catalogue: %w", err)
	}
	defer f.Close()
	index := &aquag2.Index{}
	if err := json.NewDecoder(f).Decode(index); err != nil {
		return nil, fmt.Errorf("read the catalogue as JSON: %w", err)
	}
	return index, nil
}
