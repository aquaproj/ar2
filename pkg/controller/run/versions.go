package run

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/ar2/pkg/forge"
	"github.com/aquaproj/ar2/pkg/generate"
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
func (c *Controller) versions(ctx context.Context, logger *slog.Logger, pkg *state.Package, base *aquaregistry.PackageInfo, def *definition) ([]string, error) {
	tags, err := c.listVersions(ctx, pkg, base, def)
	if err != nil {
		return nil, err
	}
	return filterVersions(logger, tags, base)
}

// listVersions reads the versions from wherever the package publishes them.
func (c *Controller) listVersions(ctx context.Context, pkg *state.Package, base *aquaregistry.PackageInfo, def *definition) ([]string, error) {
	// A package on a forge instance publishes them there, and the repository is the
	// definition's: the package name begins with the instance, and the state's copy of
	// the repository is written from what GitHub was asked for.
	if info := instanceOf(def, base); info != nil {
		return c.instanceVersions(ctx, info)
	}

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

// instanceOf is the definition of a package on a forge instance, and nil for a package on
// github.com.
//
// The branch's definition answers first, because it is what generation reads: a package
// aqua-registry still describes as an http package is generated from what its branch says
// it is.
func instanceOf(def *definition, base *aquaregistry.PackageInfo) *aquaregistry.PackageInfo {
	if def != nil && def.config != nil && generate.OnInstance(def.config.PackageInfo) {
		return def.config.PackageInfo
	}
	if generate.OnInstance(base) {
		return base
	}
	return nil
}

// instanceVersions reads the releases of a package on a forge instance.
//
// Only its releases: a tag on an instance says nothing this can download, and the
// version_source that reads tags instead is GitHub's.
func (c *Controller) instanceVersions(ctx context.Context, info *aquaregistry.PackageInfo) ([]string, error) {
	opts := &gogithub.ListOptions{PerPage: versionsPerPage}
	releases, _, err := forge.For(c.httpClient, info.Type, info.GetHost()).
		ListReleases(ctx, info.RepoOwner, info.RepoName, opts)
	if err != nil {
		return nil, fmt.Errorf("list the releases of the instance: %w", err)
	}
	versions := make([]string, 0, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		versions = append(versions, release.TagName)
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
// The second is what the definition says about which of a repository's tags are
// versions of the package at all: its version_filter, and its version_prefix, which
// every version carries when a repository releases more than one program from the same
// tags. bitwarden/clients tags the CLI cli-v2026.9.0 and the desktop application
// desktop-v2026.9.0, and only one of them is this package.
func filterVersions(logger *slog.Logger, versions []string, base *aquaregistry.PackageInfo) ([]string, error) {
	var prog *vm.Program
	prefix := ""
	if base != nil {
		prefix = base.VersionPrefix
		if base.VersionFilter != "" {
			p, err := expr.CompileVersionFilter(base.VersionFilter)
			if err != nil {
				return nil, fmt.Errorf("compile the version_filter: %w", err)
			}
			prog = p
		}
	}
	filtered := make([]string, 0, len(versions))
	for _, version := range versions {
		if genrgst.IsMovingTag(version) {
			logger.Debug("the tag moves, so it is no version of the package", "version", version)
			continue
		}
		if prefix != "" && !strings.HasPrefix(version, prefix) {
			logger.Debug("the tag doesn't carry the package's version_prefix, so it is no version of it",
				"version", version, "version_prefix", prefix)
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
