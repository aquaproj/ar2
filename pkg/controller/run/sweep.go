package run

import (
	"context"
	"log/slog"
	"maps"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/szksh-lab-2/ar2/pkg/github"
	"github.com/szksh-lab-2/ar2/pkg/state"
)

// deepCheckAge is how long a package's history stands before it is walked again.
//
// A sweep sees the newest versions, and anything published after the history is
// complete appears among them. What it can't see is a release dated in the past,
// which lands further down the list, or one of a burst bigger than the sweep looks
// at. Neither is common enough to pay for on every run and neither would ever be
// noticed otherwise.
const deepCheckAge = 7 * 24 * time.Hour

// sweep asks upstream for the newest versions of every package, in batches, and
// records what it found on each candidate.
//
// It is one query per fifty packages and costs a point each, against a budget the
// generating doesn't touch, so the whole registry is looked at every run rather than
// whatever fraction of it a run's rate limit allowed.
func (c *Controller) sweep(ctx context.Context, logger *slog.Logger, candidates []*Candidate, pkgInfos map[string]*aquaregistry.PackageInfo) map[string][]string {
	byRepo := make(map[string][]string, len(candidates))
	releases, tags := splitBySource(candidates, pkgInfos)

	for _, q := range []struct {
		name  string
		repos []github.Repo
		fn    func(context.Context, []github.Repo) (map[string][]string, map[string]string, error)
	}{
		{"releases", releases, c.graphql.Versions},
		{"tags", tags, c.graphql.Tags},
	} {
		if len(q.repos) == 0 {
			continue
		}
		found, reasons, err := q.fn(ctx, q.repos)
		if err != nil {
			logger.Warn("failed to sweep the registry", "source", q.name, "error", err.Error())
			continue
		}
		maps.Copy(byRepo, found)
		for repo, reason := range reasons {
			logger.Debug("couldn't read a package's versions", "repository", repo, "reason", reason)
		}
	}
	logger.Info("swept the registry", "num_of_packages", len(byRepo))
	return byRepo
}

// splitBySource divides the candidates by where their versions are published.
func splitBySource(candidates []*Candidate, pkgInfos map[string]*aquaregistry.PackageInfo) (releases, tags []github.Repo) {
	for _, candidate := range candidates {
		pkg := candidate.Package
		if pkg.RepoOwner == "" || pkg.RepoName == "" {
			continue
		}
		repo := github.Repo{Owner: pkg.RepoOwner, Name: pkg.RepoName}
		if base := pkgInfos[candidate.Name]; base != nil && base.VersionSource == versionSourceTag {
			tags = append(tags, repo)
			continue
		}
		releases = append(releases, repo)
	}
	return releases, tags
}

// work says what a run has to do about one package.
type work int

const (
	// workNone is a package whose newest versions are the ones the registry already
	// holds, and which hasn't gone long enough without its history being checked.
	workNone work = iota
	// workSweep is a package the sweep found something new for. The versions it
	// found are what to generate, so nothing else is asked upstream.
	workSweep
	// workDeep is a package whose whole history has to be walked: one that has
	// never been backfilled, or one whose turn it is to be checked again.
	workDeep
)

// decide says what to do about a package, given what the sweep saw.
func decide(pkg *state.Package, swept []string, now time.Time) work {
	if pkg.LastDeepCheck.IsZero() || now.Sub(pkg.LastDeepCheck) > deepCheckAge {
		return workDeep
	}
	// Nothing was swept for it: the repository is gone, renamed, or refused the
	// request. Looking is the safe way round.
	if swept == nil {
		return workDeep
	}
	if pkg.CaughtUp && pkg.Versions == state.Fingerprint(swept) {
		return workNone
	}
	return workSweep
}

// candidateVersions returns the versions to consider for a package.
//
// The sweep already asked upstream, so its answer is used as it is. Walking the
// history is the other way in, and the only one that reaches past what a sweep
// looks at.
func (c *Controller) candidateVersions(ctx context.Context, logger *slog.Logger, input *Input, candidate *Candidate, todo work, swept []string) ([]string, error) {
	if todo == workSweep {
		return filterVersions(logger, swept, input.PkgInfos[candidate.Name])
	}
	return c.versions(ctx, logger, candidate.Package, input.PkgInfos[candidate.Name])
}

// record writes down what the registry turned out to hold, so the next run can tell
// whether there is anything to do without asking.
func record(pkg *state.Package, versions []string, existing map[string]struct{}, todo work) {
	pkg.Versions = state.Fingerprint(versions)
	pkg.CaughtUp = true
	for _, v := range versions {
		if _, ok := existing[v]; !ok {
			pkg.CaughtUp = false
			break
		}
	}
	if todo == workDeep {
		pkg.LastDeepCheck = time.Now()
	}
}
