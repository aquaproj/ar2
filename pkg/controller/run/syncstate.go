package run

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/state"
)

// SyncState adds the packages aqua-registry has gained since the state was built,
// and reports whether it changed.
//
// The state is built once by 'ar2 init' and then only read. aqua-registry gains
// packages continuously, and one that isn't in the state is never ordered, so it is
// never processed at all: it would wait for the next init rather than for the next
// run. Adding it here costs one GraphQL request for however few packages are new.
//
// Star counts of packages already in the state are left alone. They go stale, but a
// stale count only shifts the order slightly, and refreshing all of them is what
// 'ar2 init' is for.
func (c *Controller) SyncState(ctx context.Context, logger *slog.Logger, s *state.State, pkgInfos map[string]*aquaregistry.PackageInfo) (bool, error) {
	added := newPackages(s, pkgInfos)
	if len(added) == 0 {
		return false, nil
	}
	logger.Info("aqua-registry has packages the state doesn't", "num_of_packages", len(added))

	repos := make([]github.Repo, 0, len(added))
	for _, pkg := range added {
		if pkg.RepoOwner == "" || pkg.RepoName == "" {
			continue
		}
		repos = append(repos, github.Repo{Owner: pkg.RepoOwner, Name: pkg.RepoName})
	}
	// A repository whose count can't be read is reported rather than fatal: the
	// package is added without one, and being processed last is better than not
	// being processed.
	stars, reasons, err := c.graphql.GetStars(ctx, repos)
	if err != nil {
		return false, fmt.Errorf("get the star counts of the new packages: %w", err)
	}
	c.graphql.FillForbiddenStars(ctx, stars, reasons)

	for name, pkg := range added {
		if repo := pkg.RepoOwner + "/" + pkg.RepoName; pkg.RepoOwner != "" {
			if star, ok := stars[repo]; ok {
				pkg.Stars = star
			} else {
				logger.Warn("failed to get the star count",
					"package", name, "repo", repo, "reason", reasons[repo])
			}
		}
		s.Packages[name] = pkg
	}
	s.UpdatedAt = time.Now()
	return true, nil
}

// newPackages returns the packages aqua-registry has and the state doesn't.
//
// They join with the fewest turns anything in the order has, which is the packages
// waiting for this lap's turn. Starting from none instead would put them ahead of the
// whole registry until they caught up -- a turn in every run for as long as that
// took, which is what counting turns is there to stop -- and starting from the most
// would cost them a lap for no reason but having arrived late. A package the registry
// has just gained has nothing generated at all, so it is the last thing to hold back,
// and it still takes one turn a lap like everything else.
//
// The counts in the order are never more than one apart, so this is a choice between
// this lap and the next. In a registry that is caught up there is no difference at
// all: a run reaches every package, so every count is the same.
func newPackages(s *state.State, pkgInfos map[string]*aquaregistry.PackageInfo) map[string]*state.Package {
	front := math.MaxInt
	for _, pkg := range s.Packages {
		front = min(front, pkg.Round)
	}
	if front == math.MaxInt {
		// An empty state, which is what the first run works from.
		front = 0
	}
	added := map[string]*state.Package{}
	for name, pkgInfo := range pkgInfos {
		if _, ok := s.Packages[name]; ok {
			continue
		}
		added[name] = &state.Package{
			RepoOwner: pkgInfo.RepoOwner,
			RepoName:  pkgInfo.RepoName,
			Round:     front,
		}
	}
	return added
}
