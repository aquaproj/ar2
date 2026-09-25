package run

import (
	"context"
	"fmt"
	"log/slog"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/expr-lang/expr/vm"
	gogithub "github.com/google/go-github/v92/github"
)

// versionsPerPage is how many versions are looked at per package in one run.
const versionsPerPage = 100

// versionSourceTag means the package's versions are its git tags rather than its
// releases. flutter is one: it tags every build and publishes the SDK elsewhere.
const versionSourceTag = "github_tag"

// versions lists the package's versions, newest first, dropping the ones
// aqua-registry says aren't versions of it.
//
// Without the filter ar2 tries to generate what aqua would never resolve. flutter
// tags 3.19.0-0.1.pre alongside 3.19.0, and its registry entry says a version is
// three numbers and nothing else; generating the former fails on a download that
// was never going to exist, and fails again on the next run, forever.
func (c *Controller) versions(ctx context.Context, logger *slog.Logger, pkg *state.Package, base *aquaregistry.PackageInfo) ([]string, error) {
	tags, err := c.listVersions(ctx, pkg, base)
	if err != nil {
		return nil, err
	}
	return filterVersions(logger, tags, base)
}

// listVersions reads the versions from wherever the package publishes them.
func (c *Controller) listVersions(ctx context.Context, pkg *state.Package, base *aquaregistry.PackageInfo) ([]string, error) {
	opts := &gogithub.ListOptions{PerPage: versionsPerPage}
	if base != nil && base.VersionSource == versionSourceTag {
		tags, _, err := c.gh.Repositories.ListTags(ctx, pkg.RepoOwner, pkg.RepoName, opts)
		if err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		versions := make([]string, 0, len(tags))
		for _, tag := range tags {
			versions = append(versions, tag.GetName())
		}
		return versions, nil
	}

	// One page is enough: a run works on the newest versions, and the older ones are
	// reached by later runs as the newest ones get recorded.
	releases, _, err := c.gh.Repositories.ListReleases(ctx, pkg.RepoOwner, pkg.RepoName, opts)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	versions := make([]string, 0, len(releases))
	for _, release := range releases {
		if release.GetDraft() || release.GetPrerelease() {
			continue
		}
		versions = append(versions, release.GetTagName())
	}
	return versions, nil
}

// filterVersions drops the tags that aren't versions of the package.
//
// Two reasons, and the first applies whether or not aqua-registry says anything. A
// tag that names a place in a release history rather than a point in it -- stable,
// latest, nightly -- moves as the repository releases, and what this generates is
// what a tag points at rather than a template resolved later: the asset name, the
// checksum, the files inside the archive. Generated for a moving tag, all of that
// stops being true the next time upstream releases, and a lock file resolved from it
// fails its checksum on every install after that.
//
// The second is aqua-registry's own version_filter, which says which of a repository's
// tags are versions of the package at all.
func filterVersions(logger *slog.Logger, versions []string, base *aquaregistry.PackageInfo) ([]string, error) {
	var prog *vm.Program
	if base != nil && base.VersionFilter != "" {
		p, err := expr.CompileVersionFilter(base.VersionFilter)
		if err != nil {
			return nil, fmt.Errorf("compile the version_filter: %w", err)
		}
		prog = p
	}
	filtered := make([]string, 0, len(versions))
	for _, version := range versions {
		if genrgst.IsMovingTag(version) {
			logger.Debug("the tag moves, so it is no version of the package", "version", version)
			continue
		}
		if prog != nil && !keepVersion(logger, prog, version) {
			continue
		}
		filtered = append(filtered, version)
	}
	return filtered, nil
}

// keepVersion evaluates the version_filter for one version.
//
// A filter that can't be evaluated for a version says nothing about it, so the
// version is kept and the generation decides. Dropping it would leave the registry
// without that version and nothing to say why.
func keepVersion(logger *slog.Logger, prog *vm.Program, version string) bool {
	ok, err := expr.EvaluateVersionFilter(logger, prog, version)
	if err != nil {
		logger.Debug("failed to evaluate the version_filter", "version", version, "error", err.Error())
		return true
	}
	return ok
}
