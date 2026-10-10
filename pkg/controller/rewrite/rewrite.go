package rewrite

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
	gogithub "github.com/google/go-github/v92/github"
)

// Branch is the head branch of the pull request that rewrites what is published.
const Branch = g2.HeadBranchPrefix + "rewrite"

const title = "chore: write the published files the way a generation writes them now"

// Repository reads the registry's trees and files.
type Repository interface {
	Subtrees(ctx context.Context, expression string) ([]string, error)
	Blobs(ctx context.Context, logger *slog.Logger, expressions []string) (map[string]string, error)
	FilesTwoDeep(ctx context.Context, expressions []string) (map[string][]string, error)
	OIDs(ctx context.Context, expressions []string) (map[string]string, error)
}

// RepoIDs reads repositories' numeric ids.
type RepoIDs interface {
	RepoIDs(ctx context.Context, repos []github.Repo) (map[string]int64, error)
}

// Writer commits and opens the pull request.
type Writer interface {
	BranchSHA(ctx context.Context, branch string) (string, error)
	CommitTree(ctx context.Context, parent, message string, entries []*gogithub.TreeEntry) (string, error)
	MoveBranch(ctx context.Context, branch, commit string) error
	CreatePullRequestFrom(ctx context.Context, logger *slog.Logger, head, base, title, body string) (*gogithub.PullRequest, error)
}

// Controller rewrites and verifies.
type Controller struct {
	repo   Repository
	ids    RepoIDs
	writer Writer
}

// New creates a Controller. writer may be nil for Verify.
func New(repo Repository, ids RepoIDs, writer Writer) *Controller {
	return &Controller{repo: repo, ids: ids, writer: writer}
}

// pkg is one package as a ref holds it.
type pkg struct {
	dir        string
	definition string
	config     *aquag2.Config
	// files is each version's registry.json, keyed by the version.
	files map[string]string
	// list is versions.json, and empty when the package holds none.
	list string
}

// rewritten is what a package is written as now: its definition, and each version's file.
func rewritten(p *pkg, id int64) (string, map[string]string, error) {
	def := p.definition
	cfg := p.config
	if id != 0 {
		d, err := Definition(p.definition, id)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", p.dir, err)
		}
		def = d
		c := *cfg
		c.RepoID = id
		cfg = &c
	}
	files := make(map[string]string, len(p.files))
	for version, content := range p.files {
		f, err := File(content, cfg, version)
		if err != nil {
			return "", nil, fmt.Errorf("%s %s: %w", p.dir, version, err)
		}
		files[version] = f
	}
	return def, files, nil
}

func idOf(p *pkg, ids map[string]int64) int64 {
	if p.config.PackageInfo == nil {
		return 0
	}
	return ids[github.Repo{Owner: p.config.RepoOwner, Name: p.config.RepoName}.String()]
}

// Rewrite opens the pull request that writes every published file the way a generation
// writes it now.
//
// Two commits: the definitions and the files first, then each package's versions.json,
// which records the sha of the versions directory it lists and so can only be written once
// a tree holds them. The pull request is squashed when it merges.
func (c *Controller) Rewrite(ctx context.Context, logger *slog.Logger, dryRun bool) error {
	pkgs, err := c.load(ctx, logger, g2.DefaultBranch)
	if err != nil {
		return err
	}
	ids, err := c.repoIDs(ctx, pkgs)
	if err != nil {
		return err
	}
	entries, renderedFiles, err := render(logger, pkgs, ids)
	if err != nil {
		return err
	}
	logger.Info("rewriting the published files", "num_of_packages", len(pkgs), "num_of_files", len(entries))
	if dryRun || len(entries) == 0 {
		return nil
	}

	return c.commit(ctx, logger, pkgs, entries, renderedFiles)
}

// render is what every package is written as now: the tree entries of the files that change,
// and every version's rendered file, by package.
func render(logger *slog.Logger, pkgs map[string]*pkg, ids map[string]int64) ([]*gogithub.TreeEntry, map[string]map[string]string, error) {
	entries := []*gogithub.TreeEntry{}
	renderedFiles := make(map[string]map[string]string, len(pkgs))
	for _, dir := range slices.Sorted(maps.Keys(pkgs)) {
		p := pkgs[dir]
		id := idOf(p, ids)
		if id == 0 {
			logger.Warn("no repository id for the package; its definition is left without one", "dir", dir)
		}
		def, files, err := rewritten(p, id)
		if err != nil {
			return nil, nil, err
		}
		renderedFiles[dir] = files
		if def != p.definition {
			entries = append(entries, blob(dir+"/"+g2.ConfigFileName, def))
		}
		for _, version := range slices.Sorted(maps.Keys(files)) {
			if files[version] != p.files[version] {
				entries = append(entries, blob(dir+"/"+aquag2.Path(version), files[version]))
			}
		}
	}
	return entries, renderedFiles, nil
}

// commit writes the files, then the lists made from them, and opens the pull request.
func (c *Controller) commit(ctx context.Context, logger *slog.Logger, pkgs map[string]*pkg, entries []*gogithub.TreeEntry, files map[string]map[string]string) error {
	parent, err := c.writer.BranchSHA(ctx, g2.DefaultBranch)
	if err != nil {
		return err //nolint:wrapcheck
	}
	first, err := c.writer.CommitTree(ctx, parent, title, entries)
	if err != nil {
		return fmt.Errorf("commit the files: %w", err)
	}
	lists, err := c.lists(ctx, logger, first, pkgs, files)
	if err != nil {
		return err
	}
	second, err := c.writer.CommitTree(ctx, first, "chore: list the versions again", lists)
	if err != nil {
		return fmt.Errorf("commit the lists of versions: %w", err)
	}
	if err := c.writer.MoveBranch(ctx, Branch, second); err != nil {
		return err //nolint:wrapcheck
	}
	pr, err := c.writer.CreatePullRequestFrom(ctx, logger, Branch, g2.DefaultBranch, title, body(len(pkgs), len(entries)))
	if err != nil {
		return err //nolint:wrapcheck
	}
	logger.Info("opened the pull request", "number", pr.GetNumber())
	return nil
}

// lists renders each package's versions.json from its files, made from the versions
// directory the commit holds.
func (c *Controller) lists(ctx context.Context, logger *slog.Logger, commit string, pkgs map[string]*pkg, files map[string]map[string]string) ([]*gogithub.TreeEntry, error) {
	expr := func(dir string) string { return commit + ":" + dir + "/" + aquag2.VersionDir }
	exprs := []string{}
	for dir, p := range pkgs {
		if len(p.files) > 0 {
			exprs = append(exprs, expr(dir))
		}
	}
	oids, err := c.repo.OIDs(ctx, exprs)
	if err != nil {
		return nil, fmt.Errorf("read the versions directories: %w", err)
	}
	entries := []*gogithub.TreeEntry{}
	for _, dir := range slices.Sorted(maps.Keys(pkgs)) {
		if len(pkgs[dir].files) == 0 {
			continue
		}
		list, err := g2.RenderVersionsList(logger, oids[expr(dir)], files[dir])
		if err != nil {
			return nil, err //nolint:wrapcheck
		}
		entries = append(entries, blob(dir+"/"+aquag2.VersionsFileName, list))
	}
	return entries, nil
}

func blob(p, content string) *gogithub.TreeEntry {
	return &gogithub.TreeEntry{Path: new(p), Mode: new("100644"), Type: new("blob"), Content: new(content)}
}

func body(pkgs, files int) string {
	return fmt.Sprintf(`Writes the %d files of the registry's %d packages that a generation would write differently now (aquaproj/aqua-registry-g2#743):

- each definition records its repository's id, as `+"`repo_id`"+` after `+"`repo_name`"+`;
- each registry.json records it on the entries naming that repository, has cosign's command line in the fields that say it, and is indented;
- each versions.json is written again, since every digest changed.

Nothing about a release is read again, so the checks don't download anything: `+"`ar2 rewrite --verify`"+` derives every file again from what it replaces and compares the bytes.
`, files, pkgs)
}

// repoIDs reads the id of every package's repository.
func (c *Controller) repoIDs(ctx context.Context, pkgs map[string]*pkg) (map[string]int64, error) {
	repos := []github.Repo{}
	seen := map[string]struct{}{}
	for _, p := range pkgs {
		if p.config.PackageInfo == nil || p.config.RepoOwner == "" || p.config.RepoName == "" {
			continue
		}
		r := github.Repo{Owner: p.config.RepoOwner, Name: p.config.RepoName}
		if _, ok := seen[r.String()]; ok {
			continue
		}
		seen[r.String()] = struct{}{}
		repos = append(repos, r)
	}
	ids, err := c.ids.RepoIDs(ctx, repos)
	if err != nil {
		return nil, fmt.Errorf("read the repositories' ids: %w", err)
	}
	return ids, nil
}
