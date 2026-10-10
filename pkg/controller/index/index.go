// Package index maintains aqua-registry-g2's catalogue of the packages it holds.
//
// Everything else about a package lives on its own branch, which is what keeps one
// package's history out of another's. Searching can't work that way: it reads a name
// and a description for every package before knowing which one is wanted. So the
// catalogue is a single file on the default branch, and it has to be kept in step
// with the branches by hand.
package index

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
	"go.yaml.in/yaml/v3"
)

// Registry is what the catalogue is built from and written to.
type Registry interface {
	Index(ctx context.Context, ref string) (*aquag2.Index, error)
	File(ctx context.Context, ref, path string) (string, error)
	Config(ctx context.Context, pkgName string) (*aquag2.Config, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	IndexPullRequest(ctx context.Context) (*gogithub.PullRequest, error)
	CreateIndexPullRequest(ctx context.Context, logger *slog.Logger, base, title, body string) (*gogithub.PullRequest, error)
}

// AutoMerger turns a pull request over to its checks.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, pullRequestID string) error
}

// Controller keeps the catalogue in step with the package branches.
type Controller struct {
	g2         Registry
	automerge  AutoMerger
	baseBranch string
	// defs is the definition on every package branch, which is also what the identities
	// were read out of. The reconciliation needs all of them: a definition edited after
	// the entry was made from it is a difference nothing else would notice. The paths
	// that bring one package -- a run taking a package over, a rename -- pass nil.
	defs map[string]string
	// now is what an identifier is minted from. A field so that a test can mint a
	// known one.
	now func() time.Time
}

// New creates a Controller. automerge may be nil, and then the catalogue's pull
// requests wait for someone; defs may be nil in anything but Sync.
func New(registry Registry, automerge AutoMerger, baseBranch string, defs map[string]string) *Controller {
	return &Controller{
		g2:         registry,
		automerge:  automerge,
		baseBranch: baseBranch,
		defs:       defs,
		now:        time.Now,
	}
}

// AddPackage puts one package into the catalogue.
//
// This is the path a run takes as it takes a package over: it has just built the
// definition, so nothing is read back and no branches are listed. The definition is
// passed in because the branch doesn't hold it yet — it is in the pull request that
// brings the package, which is the moment the catalogue can first describe it.
func (c *Controller) AddPackage(ctx context.Context, logger *slog.Logger, pkgName string, cfg *aquag2.Config) error {
	return c.addOne(ctx, logger, pkgName, cfg)
}

// Rename lists a package under its new name and stops listing it under the old one.
//
// Both in one commit. A catalogue holding the old name as a package and the new one as a
// package whose alias is that name says two things about one name, and nothing can decide
// which of them answers -- which is a state it is checked for rather than resolved.
func (c *Controller) Rename(ctx context.Context, logger *slog.Logger, from, to string, cfg *aquag2.Config) error {
	t, err := c.read(ctx)
	if err != nil {
		return err
	}
	entries := entriesByName(t.index)
	t.index.Remove(from)
	t.index.Remove(to)
	logger.Info("listing the package under its new name", "package", from, "renamed_to", to)
	entry := aquag2.NewIndexPackage(to, cfg)
	// The identifier of the name it had. A rename is the same package under another
	// name, and what the branch is named after doesn't change with the name.
	c.identify(entry, entries[from], ids(t.index))
	return c.write(ctx, logger, t, &change{
		added: []*aquag2.IndexPackage{entry},
	})
}

// Refresh reads the named packages' entries out of their definitions again.
//
// An entry is made once, from the definition the package arrived with, and a definition
// edited afterwards leaves it describing the package as it was. Nothing notices: the
// reconciliation asks which packages the catalogue is missing, and this one isn't.
//
// The aliases matter most of what an entry holds. They are what a configuration still
// asking for an old name resolves through, and the table beside the catalogue is
// rendered from them in the same commit, so an alias added by hand reaches aqua only
// once this has run.
//
// Which packages, rather than all of them: a definition is a request per package, and
// finding the edited one by reading every definition in the registry would be thousands
// of requests to catch something a person did and knows the name of. A package the
// catalogue doesn't list yet is added, which is what 'ar2 index' would do with it
// anyway.
func (c *Controller) Refresh(ctx context.Context, logger *slog.Logger, pkgNames []string) error {
	t, err := c.read(ctx)
	if err != nil {
		return err
	}
	have := entriesByName(t.index)
	taken := ids(t.index)
	entries := make([]*aquag2.IndexPackage, 0, len(pkgNames))
	for _, pkgName := range pkgNames {
		cfg, err := c.g2.Config(ctx, pkgName)
		if err != nil {
			return fmt.Errorf("get the definition of %s: %w", pkgName, err)
		}
		if cfg == nil {
			// Nothing to describe the package with. Named explicitly, this is a name
			// that is wrong rather than a package waiting for its first run, so it is
			// said rather than skipped.
			return fmt.Errorf("%w: %s", errNoDefinition, pkgName)
		}
		logger.Info("reading the package's entry from its definition again", "package", pkgName)
		// The entry is replaced rather than edited: what it holds is a projection of
		// the definition, so what the definition says now is the whole of it.
		t.index.Remove(pkgName)
		entry := aquag2.NewIndexPackage(pkgName, cfg)
		c.identify(entry, have[pkgName], taken)
		entries = append(entries, entry)
	}
	return c.write(ctx, logger, t, &change{updated: entries})
}

// Sync makes the catalogue say what the package branches say.
//
// Two things it catches, and they used to need two different answers. A package whose run
// failed after committing its definition, or whose pull request was never merged, is
// missing from the catalogue; and a package whose definition was edited after its entry
// was made has an entry describing it as it was. Asking what the catalogue is missing
// finds the first and can't find the second.
//
// So every definition is read and every entry is compared against the one its definition
// makes now. It costs what listing the branches cost, because the definitions come back
// with them: sixteen queries for the whole registry, where fetching a definition for each
// package the catalogue lacked was a request apiece.
//
// Nothing is removed. An entry with no definition behind it is either a package waiting
// for the pull request that brings its definition -- which a run has already added to the
// catalogue, and removing it would undo -- or an orphan, which is somebody's decision
// rather than a reconciliation's.
func (c *Controller) Sync(ctx context.Context, logger *slog.Logger) error {
	if c.defs == nil {
		return errNoDefinitions
	}
	logger.Info("read the definitions on the package branches", "num_of_definitions", len(c.defs))

	t, err := c.read(ctx)
	if err != nil {
		return err
	}
	return c.write(ctx, logger, t, c.reconcile(logger, t.index, c.defs))
}

// reconcile works out what the catalogue has to gain and what it has to say differently.
func (c *Controller) reconcile(logger *slog.Logger, index *aquag2.Index, files map[string]string) *change {
	have := entriesByName(index)
	ch := &change{}
	// In branch order, so that a pull request reads the same way whatever order the
	// branches came back in.
	for _, branch := range slices.Sorted(maps.Keys(files)) {
		id, ok := g2.BranchID(branch)
		if !ok {
			// A branch still named after the package. Reading it would list the
			// package twice, under the same name.
			continue
		}
		cfg := parseConfig(logger, branch, files[branch])
		if cfg == nil {
			continue
		}
		if g2.IsClaim(cfg) {
			// The claim the branch was created with. The package is waiting for the
			// pull request that brings its definition, and there is nothing to
			// describe it with until then.
			logger.Debug("the branch holds no definition yet", "branch", branch)
			continue
		}
		pkgName := cfg.Name
		if pkgName == "" {
			// Nothing says which package the branch holds: its name is an id and its
			// definition doesn't answer for itself.
			logger.Warn("a package branch holds a definition that doesn't name its package",
				"branch", branch)
			continue
		}
		entry := aquag2.NewIndexPackage(pkgName, cfg)
		// The branch the definition was read from is the id. Nothing is minted here:
		// the branch exists, so what the catalogue has to say about where the package
		// is has an answer already, and minting would give a second one.
		entry.ID = id
		old, ok := have[pkgName]
		if !ok {
			logger.Info("adding a package to the catalogue", "package", pkgName)
			ch.added = append(ch.added, entry)
			continue
		}
		if sameEntry(old, entry) {
			continue
		}
		logger.Info("the package's definition says something else now", "package", pkgName)
		// Replaced rather than edited: what an entry holds is a projection of the
		// definition, so what the definition says now is the whole of it.
		index.Remove(pkgName)
		ch.updated = append(ch.updated, entry)
	}
	return ch
}

// parseConfig reads one branch's definition, or nothing when the branch holds nothing
// this can describe a package with.
//
// A definition that doesn't parse is reported and skipped rather than failing the run. It
// is one package's file, and stopping here would leave the catalogue no closer to what
// every other branch says.
func parseConfig(logger *slog.Logger, branch, content string) *aquag2.Config {
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		slogerr.WithError(logger, err).Warn("read a package definition as YAML", "branch", branch)
		return nil
	}
	if cfg.PackageInfo == nil {
		// A file that is YAML but not a definition. Describing the package from it
		// would say nothing but its name, which is worse than leaving the entry alone.
		logger.Warn("a package branch holds no definition", "branch", branch)
		return nil
	}
	return cfg
}

// identify gives the entry the identifier the catalogue already has for the package, and
// mints one when it has none.
//
// The identifier is what the package's branch will be named after, so it outlives every
// name the package is listed under: an entry rebuilt from a definition keeps it, and a
// rename carries it to the new name. What it must never do is change, which is why this is
// the only place that decides it.
//
// A package the catalogue lists without one is a package taken over before the registry
// minted them. Minting here is what fills those in, and it is what makes the entry differ
// from the one already written, so the reconciliation commits it.
func (c *Controller) identify(entry, old *aquag2.IndexPackage, taken map[string]struct{}) {
	if old != nil && old.ID != "" {
		entry.ID = old.ID
		return
	}
	entry.ID = g2.MintID(c.now(), taken)
	taken[entry.ID] = struct{}{}
}

// ids is the identifiers the catalogue has spoken for, which is what a new one is minted
// against.
func ids(index *aquag2.Index) map[string]struct{} {
	out := make(map[string]struct{}, len(index.Packages))
	for _, pkg := range index.Packages {
		if pkg != nil && pkg.ID != "" {
			out[pkg.ID] = struct{}{}
		}
	}
	return out
}

// entriesByName is the catalogue's entries, by the package they describe.
func entriesByName(index *aquag2.Index) map[string]*aquag2.IndexPackage {
	entries := make(map[string]*aquag2.IndexPackage, len(index.Packages))
	for _, pkg := range index.Packages {
		if pkg == nil {
			continue
		}
		entries[pkg.Name] = pkg
	}
	return entries
}

// sameEntry reports whether the catalogue already says what the definition says.
//
// Field by field rather than by rendering both, because what is being asked is whether
// this entry has to be replaced, and the rendering of the whole catalogue is what decides
// whether anything is committed.
func sameEntry(a, b *aquag2.IndexPackage) bool {
	return a.Name == b.Name &&
		a.ID == b.ID &&
		a.Description == b.Description &&
		a.Link == b.Link &&
		slices.Equal(a.Aliases, b.Aliases) &&
		slices.Equal(a.SearchWords, b.SearchWords)
}
