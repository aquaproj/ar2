// Package dates fills in, for the versions the registry already holds, when the release
// each came from was published.
//
// registry.json has said it since ar2 v0.5.0, and the files written before that don't.
// Nothing can work it out from them: the version string doesn't say it, and for a package
// whose tags aren't semver there is nothing else to order releases by at all. So it is
// read off the releases and written in.
//
// This is not a regeneration. Nothing about a version is generated again -- no asset is
// downloaded, no definition is consulted, nothing already in the file is touched -- which
// is what makes it a push onto the package branch rather than a pull request per package:
// the diff of each file is one field, and the release it was read from is what says
// whether it is right.
package dates

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// Registry is the part of aqua-registry-g2 this reads and writes.
type Registry interface {
	Branch(pkgName string) (string, bool)
	BranchSHA(ctx context.Context, branch string) (string, error)
	VersionsOnRef(ctx context.Context, logger *slog.Logger, ref string) (map[string]struct{}, error)
	File(ctx context.Context, ref, path string) (string, error)
	Push(ctx context.Context, branch, parent, message string, files []*g2.File) error
}

// Releases lists the releases of an upstream repository.
type Releases interface {
	ListReleases(ctx context.Context, owner, repo string, opts *gogithub.ListOptions) ([]*gogithub.RepositoryRelease, *gogithub.Response, error)
}

// Controller fills in the dates.
type Controller struct {
	registry Registry
	releases Releases
	// defs is each package branch's definition, keyed by branch name. It is what says
	// which package a branch holds and where its versions come from.
	defs map[string]string
}

// New creates a Controller.
func New(registry Registry, releases Releases, defs map[string]string) *Controller {
	return &Controller{registry: registry, releases: releases, defs: defs}
}

// Args holds what one run does.
type Args struct {
	// Packages narrows the run to the packages named. Naming none does every package.
	Packages []string
	// Limit bounds how many packages one run writes to, 0 for as many as there are.
	Limit int
	// DryRun says what would be written without writing it.
	DryRun bool
}

// Fill works through the packages, filling in the date of every version that hasn't one.
//
// A package already filled in costs the reads that establish so and no write, so a second
// run over a registry that is done is idempotent rather than a no-op by luck.
func (c *Controller) Fill(ctx context.Context, logger *slog.Logger, args *Args) error {
	filled := 0
	for _, pkg := range c.packages(logger, args.Packages) {
		if args.Limit > 0 && filled >= args.Limit {
			logger.Info("stopping at the limit", "limit", args.Limit)
			return nil
		}
		wrote, err := c.fill(ctx, logger.With("package", pkg.name), pkg, args.DryRun)
		if err != nil {
			// One package failing says nothing about the others, and the registry is
			// better off with the dates it can have than with none.
			logger.Warn("failed to fill in the dates", "package", pkg.name, "error", err.Error())
			continue
		}
		if wrote {
			filled++
		}
	}
	return nil
}

// pkg is a package to fill in: what it is called, and the repository its releases are in.
type pkg struct {
	name   string
	branch string
	owner  string
	repo   string
}

// packages is what the run works through, in name order so that two runs of the same
// registry do the same thing in the same order.
//
// A package whose versions are tags is left out. Its versions aren't releases, so the
// release list doesn't hold them, and listing it would be requests spent to find nothing.
func (c *Controller) packages(logger *slog.Logger, named []string) []*pkg {
	want := make(map[string]struct{}, len(named))
	for _, name := range named {
		want[name] = struct{}{}
	}
	out := make([]*pkg, 0, len(c.defs))
	for branch, def := range c.defs {
		p, ok := packageOf(logger, branch, def, want)
		if !ok {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *pkg) int { return strings.Compare(a.name, b.name) })
	return out
}

// packageOf is the package a branch holds, and false when it isn't one to fill in.
//
// A branch with only its claim on it holds no version yet. A package that isn't released
// on GitHub, or whose versions are its tags, has no release list to read a date from.
func packageOf(logger *slog.Logger, branch, def string, want map[string]struct{}) (*pkg, bool) {
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(def), cfg); err != nil || cfg.PackageInfo == nil {
		logger.Debug("a branch holds no definition to read", "branch", branch)
		return nil, false
	}
	if cfg.Name == "" || g2.IsClaim(cfg) {
		return nil, false
	}
	if len(want) > 0 {
		if _, ok := want[cfg.Name]; !ok {
			return nil, false
		}
	}
	if cfg.VersionSource != "" || (cfg.Type != "" && cfg.Type != aquaregistry.PkgInfoTypeGitHubRelease) {
		return nil, false
	}
	return &pkg{name: cfg.Name, branch: branch, owner: cfg.RepoOwner, repo: cfg.RepoName}, true
}

// fill fills in the dates of one package, and reports whether anything was written.
func (c *Controller) fill(ctx context.Context, logger *slog.Logger, p *pkg, dryRun bool) (bool, error) {
	sha, err := c.registry.BranchSHA(ctx, p.branch)
	if err != nil {
		return false, err //nolint:wrapcheck // the error names the branch
	}
	if sha == "" {
		return false, nil
	}
	files, err := c.files(ctx, logger, p)
	if err != nil || len(files) == 0 {
		return false, err
	}
	logger.Info("filling in when the releases were published", "versions", len(files))
	if dryRun {
		return true, nil
	}
	if err := c.registry.Push(ctx, p.branch, sha, message(p.name, len(files)), files); err != nil {
		return false, fmt.Errorf("push the dates onto the package branch: %w", err)
	}
	return true, nil
}

// files is what the package's branch would be written with: the file of every version
// that doesn't say when it was published, with the date of its release in it.
func (c *Controller) files(ctx context.Context, logger *slog.Logger, p *pkg) ([]*g2.File, error) {
	versions, err := c.registry.VersionsOnRef(ctx, logger, p.branch)
	if err != nil {
		return nil, fmt.Errorf("list the versions the branch holds: %w", err)
	}
	held, err := c.undated(ctx, logger, p, versions)
	if err != nil {
		return nil, err
	}
	if len(held) == 0 {
		logger.Debug("every version says when it was published")
		return nil, nil
	}
	published, err := c.published(ctx, p, held)
	if err != nil {
		return nil, err
	}
	return dated(logger, held, published)
}

// dated is the files to write: each version's own file with the date of its release in it.
func dated(logger *slog.Logger, held []*undatedVersion, published map[string]string) ([]*g2.File, error) {
	files := make([]*g2.File, 0, len(held))
	for _, v := range held {
		at, ok := published[v.version]
		if !ok {
			// The release is gone, or was never one. What the file says is then
			// unknown rather than wrong, and saying nothing is what it already does.
			logger.Warn("the repository has no release for this version", "version", v.version)
			continue
		}
		v.registry.PublishedAt = at
		content, err := marshal(v.registry)
		if err != nil {
			return nil, err
		}
		files = append(files, &g2.File{Path: aquag2.Path(v.version), Content: content})
	}
	return files, nil
}

// undatedVersion is a version the registry holds that doesn't say when it was published.
type undatedVersion struct {
	version  string
	registry *aquag2.Registry
}

// undated is the versions whose file doesn't say when it was published.
func (c *Controller) undated(ctx context.Context, logger *slog.Logger, p *pkg, versions map[string]struct{}) ([]*undatedVersion, error) {
	out := make([]*undatedVersion, 0, len(versions))
	for version := range versions {
		content, err := c.registry.File(ctx, p.branch, aquag2.Path(version))
		if err != nil {
			return nil, fmt.Errorf("read the registry.json of %s: %w", version, err)
		}
		if content == "" {
			continue
		}
		reg := &aquag2.Registry{}
		if err := json.Unmarshal([]byte(content), reg); err != nil {
			// A file that can't be read is not one to rewrite from a struct that
			// didn't understand it.
			logger.Warn("failed to read a registry.json", "version", version, "error", err.Error())
			continue
		}
		if reg.PublishedAt != "" {
			continue
		}
		out = append(out, &undatedVersion{version: version, registry: reg})
	}
	slices.SortFunc(out, func(a, b *undatedVersion) int { return strings.Compare(a.version, b.version) })
	return out, nil
}

// releasesPerPage is how many releases one request lists.
const releasesPerPage = 100

// published is when each of the given versions was published, keyed by tag.
//
// The release list is walked rather than each release fetched by tag: a hundred come back
// in one request where a hundred tags would be a hundred. It stops as soon as every
// version asked about has been found, so a package with a long history and a few undated
// versions near the top of it costs one page.
func (c *Controller) published(ctx context.Context, p *pkg, held []*undatedVersion) (map[string]string, error) {
	want := make(map[string]struct{}, len(held))
	for _, v := range held {
		want[v.version] = struct{}{}
	}
	out := make(map[string]string, len(held))
	opts := &gogithub.ListOptions{PerPage: releasesPerPage}
	for {
		releases, resp, err := c.releases.ListReleases(ctx, p.owner, p.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list the releases of %s/%s: %w", p.owner, p.repo, err)
		}
		for _, release := range releases {
			tag := release.GetTagName()
			if _, ok := want[tag]; !ok {
				continue
			}
			if at := publishedAt(release.GetPublishedAt().Time); at != "" {
				out[tag] = at
			}
			delete(want, tag)
		}
		if len(want) == 0 || resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// publishedAt is when a release was published, written the way the generated file writes
// it: RFC 3339, in UTC. A release with no such moment is left empty rather than dated to
// the beginning of the epoch.
func publishedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// marshal renders registry.json the way a generation stores it, so that a file filled in
// here and the same file generated again are the same bytes.
func marshal(reg *aquag2.Registry) (string, error) {
	b, err := json.Marshal(reg)
	if err != nil {
		return "", fmt.Errorf("marshal registry.json: %w", err)
	}
	return string(b) + "\n", nil
}

// message is what the commit says.
func message(pkgName string, versions int) string {
	if versions == 1 {
		return fmt.Sprintf("chore(%s): record when its release was published", pkgName)
	}
	return fmt.Sprintf("chore(%s): record when %d releases were published", pkgName, versions)
}
