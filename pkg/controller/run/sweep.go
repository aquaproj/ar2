package run

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/aquaproj/ar2/pkg/summary"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
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
// The second result is the repositories that answer to another name now, keyed by the
// name the registry has for them.
//
// It is one query per fifty packages and costs a point each, against a budget the
// generating doesn't touch, so the whole registry is looked at every run rather than
// whatever fraction of it a run's rate limit allowed.
func (c *Controller) sweep(ctx context.Context, logger *slog.Logger, candidates []*Candidate, pkgInfos map[string]*aquaregistry.PackageInfo) (map[string][]string, map[string]string) {
	byRepo := make(map[string][]string, len(candidates))
	renamed := map[string]string{}
	releases, tags := splitBySource(candidates, pkgInfos)

	for _, q := range []struct {
		name  string
		repos []github.Repo
		fn    func(context.Context, []github.Repo) (*github.Sweep, error)
	}{
		{"releases", releases, c.graphql.Versions},
		{"tags", tags, c.graphql.Tags},
	} {
		if len(q.repos) == 0 {
			continue
		}
		found, err := q.fn(ctx, q.repos)
		if err != nil {
			logger.Warn("failed to sweep the registry", "source", q.name, "error", err.Error())
			continue
		}
		maps.Copy(byRepo, found.Versions)
		maps.Copy(renamed, found.Names)
		for repo, reason := range found.Reasons {
			logger.Debug("couldn't read a package's versions", "repository", repo, "reason", reason)
		}
	}
	logger.Info("swept the registry", "num_of_packages", len(byRepo))
	c.reportRenamed(logger, renamed)
	return byRepo, renamed
}

// reportRenamed says which repositories aren't where the registry says they are.
//
// The sweep asks every repository what it is called and GitHub answers a query made
// with an old name, so this is found without asking anything extra. Nothing is done
// about it yet: the branch holding the package's generated versions is named after the
// old name, and moving it is a run of its own.
//
// Said here rather than left in the log because it is the registry being out of date
// about what a package is, which is the kind of thing that stays that way until
// somebody reads it.
func (c *Controller) reportRenamed(logger *slog.Logger, renamed map[string]string) {
	if len(renamed) == 0 {
		return
	}
	for _, from := range slices.Sorted(maps.Keys(renamed)) {
		logger.Warn("the repository has been renamed", "repository", from, "renamed_to", renamed[from])
		c.renamed = append(c.renamed, &summary.Rename{From: from, To: renamed[from]})
	}
}

// moveRenamed puts the packages of a renamed repository under the names they have now.
//
// One repository can hold several packages -- kubernetes/kubernetes publishes ten
// commands -- so a rename of it is a rename of each of them, and the new name is the old
// one with the repository replaced. A package whose name doesn't begin with its
// repository can't be rewritten that way and is left for a person: the summary says the
// repository moved, and 'ar2 rename' takes the two names.
//
// It returns the packages to leave alone for the rest of the run. Their branch is under
// the new name now, and what the state says about them is the new name too, so generating
// them here would be generating under a name nothing holds.
func (c *Controller) moveRenamed(ctx context.Context, logger *slog.Logger, input *Input, renamed map[string]string) map[string]struct{} {
	if c.renamer == nil || len(renamed) == 0 {
		return nil
	}
	moved := map[string]struct{}{}
	for _, name := range slices.Sorted(maps.Keys(input.State.Packages)) {
		pkg := input.State.Packages[name]
		repo := pkg.RepoOwner + "/" + pkg.RepoName
		to, ok := renamed[repo]
		if !ok {
			continue
		}
		newName, ok := rename(name, repo, to)
		if !ok {
			logger.Warn("the repository moved but the package's name doesn't say where",
				"package", name, "repository", repo, "renamed_to", to)
			continue
		}
		if err := c.renamer.Rename(ctx, logger, name, newName); err != nil {
			// The next run sweeps the same repository and finds the same rename, so
			// this is worth reporting rather than stopping for.
			slogerr.WithError(logger, err).Warn("failed to move the package to its new name",
				"package", name, "renamed_to", newName)
			continue
		}
		input.State.Rename(name, newName)
		moved[name] = struct{}{}
	}
	return moved
}

// rename is the package's name with its repository replaced.
//
// A package is usually its repository and sometimes a command inside one, so what follows
// the repository is kept: the kubectl of kubernetes/kubernetes stays the kubectl of
// wherever that repository went.
func rename(pkgName, repo, to string) (string, bool) {
	if pkgName == repo {
		return to, true
	}
	if rest, ok := strings.CutPrefix(pkgName, repo+"/"); ok {
		return to + "/" + rest, true
	}
	return "", false
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
