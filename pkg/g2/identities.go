package g2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// Identities is the id each package is kept under.
//
// An id doesn't say which package it is, so this is read from the packages themselves: the
// directory carries the id and the definition in it carries the package's name. Every
// package answers for itself, where a table kept anywhere else -- the catalogue, say --
// could disagree with what the directories hold and send a package's versions to another
// package's directory.
//
// A package the table doesn't have is one the registry doesn't hold yet. Taking it over
// mints its id, and the pull request that brings its definition is where the next reading
// finds it.
type Identities struct {
	// byName is the package's name to its id.
	byName map[string]string
	// taken is every id a directory or an open pull request is named after, including
	// the ones whose definition couldn't be read. An id is minted by stepping past what is
	// taken, so leaving one out here would hand out an id a package already has.
	taken map[string]struct{}
	// byID is the other way round, for a caller that has an id and wants the package.
	byID map[string]string
	// now is what an id is minted from. A field so that a test can mint a known one.
	now func() time.Time
}

// errNoIdentity says the registry holds no package of that name, so there is nowhere to
// write it. A package is taken over by minting its id.
var errNoIdentity = errors.New("the registry holds no such package")

// errNoIdentities says the client was never told which id each package is kept under, so
// it has nowhere to put a package. It is a wiring mistake rather than something about the
// registry, which is why minting says it instead of handing out an id into the dark.
var errNoIdentities = errors.New("the client wasn't told which id each package is kept under")

// Definitions reads what the identities are read out of.
type Definitions interface {
	// Subtrees is the directories two levels under the tree an expression names.
	Subtrees(ctx context.Context, expression string) ([]string, error)
	// Blobs is the text of the file each expression names, keyed by the expression.
	Blobs(ctx context.Context, logger *slog.Logger, expressions []string) (map[string]string, error)
	// OpenPullRequestHeads is the head branch of every open pull request.
	OpenPullRequestHeads(ctx context.Context) ([]string, error)
}

// ReadIdentities reads which id every package is kept under, and returns the definition
// each package on the default branch holds, keyed by id, along with it.
//
// The definitions come back because they are what the table was read out of: a caller that
// works through every definition -- the tidy, the reconciliation -- would otherwise read
// them again.
//
// A package whose definition is still in an open pull request is in the table too. Its id
// was minted by the run that opened the pull request, and nothing is on the default branch
// for it until that merges, so without reading the pull request the next run would mint
// it a second id and open a second pull request.
func ReadIdentities(ctx context.Context, logger *slog.Logger, defs Definitions) (*Identities, map[string]string, error) {
	dirs, err := defs.Subtrees(ctx, DefaultBranch+":"+PackagesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("list the packages: %w", err)
	}
	ids := packageIDs(logger, dirs)
	held, err := readDefinitions(ctx, logger, defs, DefaultBranch, ids)
	if err != nil {
		return nil, nil, err
	}

	pending, err := pendingDefinitions(ctx, logger, defs, ids)
	if err != nil {
		return nil, nil, err
	}
	return NewIdentities(logger, held, pending), held, nil
}

// pendingDefinitions is the definition each open pull request brings for a package the
// default branch doesn't hold, keyed by id. A pull request whose definition can't be read
// is there with an empty one, because it takes the id all the same.
func pendingDefinitions(ctx context.Context, logger *slog.Logger, defs Definitions, held []string) (map[string]string, error) {
	heads, err := defs.OpenPullRequestHeads(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error says what it couldn't list
	}
	onMain := make(map[string]struct{}, len(held))
	for _, id := range held {
		onMain[id] = struct{}{}
	}
	ids := []string{}
	exprs := []string{}
	for _, head := range slices.Sorted(slices.Values(heads)) {
		id, ok := HeadBranchID(head)
		if !ok {
			continue
		}
		if _, ok := onMain[id]; ok {
			continue
		}
		ids = append(ids, id)
		exprs = append(exprs, head+":"+PackageDir(id)+"/"+ConfigFileName)
	}
	blobs, err := defs.Blobs(ctx, logger, exprs)
	if err != nil {
		return nil, fmt.Errorf("read the definitions in open pull requests: %w", err)
	}
	pending := map[string]string{}
	for i, id := range ids {
		// A package can have more than one pull request open, its own and one per version
		// waiting for a definition. The first that holds the definition says it.
		if pending[id] != "" {
			continue
		}
		pending[id] = blobs[exprs[i]]
	}
	return pending, nil
}

// packageIDs is the ids of the package directories, from their paths under PackagesDir.
//
// A directory that isn't where its id says it is would never be found by anything that
// addresses the package, so it is reported rather than read.
func packageIDs(logger *slog.Logger, dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		shard, id, _ := strings.Cut(dir, "/")
		if !IsID(id) || Shard(id) != shard {
			logger.Warn("a directory under "+PackagesDir+" isn't a package", "path", PackagesDir+"/"+dir)
			continue
		}
		out = append(out, id)
	}
	return out
}

// readDefinitions reads the definition of each package on a ref, keyed by id. A package
// whose definition isn't there is left out.
func readDefinitions(ctx context.Context, logger *slog.Logger, defs Definitions, ref string, ids []string) (map[string]string, error) {
	exprs := make([]string, len(ids))
	for i, id := range ids {
		exprs[i] = ref + ":" + PackageDir(id) + "/" + ConfigFileName
	}
	blobs, err := defs.Blobs(ctx, logger, exprs)
	if err != nil {
		return nil, fmt.Errorf("read the definitions: %w", err)
	}
	out := make(map[string]string, len(ids))
	for i, id := range ids {
		text, ok := blobs[exprs[i]]
		if !ok {
			continue
		}
		out[id] = text
	}
	return out, nil
}

// NewIdentities reads the table out of the definitions, keyed by id: held is what the
// default branch holds and pending what open pull requests bring.
//
// What the default branch says wins. A pull request naming a package the default branch
// already holds under another id is one that would add a second copy of it, and is left to
// the review it is waiting for.
func NewIdentities(logger *slog.Logger, held, pending map[string]string) *Identities {
	ids := &Identities{
		byName: make(map[string]string, len(held)+len(pending)),
		byID:   make(map[string]string, len(held)+len(pending)),
		taken:  make(map[string]struct{}, len(held)+len(pending)),
		now:    time.Now,
	}
	ids.add(logger, held)
	ids.add(logger, pending)
	return ids
}

// packageOn is the package a definition says it is.
//
// The definition is the only thing that says it, since the directory is named after an id,
// so a definition that doesn't name a package leaves the id unaddressable. That is reported
// rather than guessed at: the guess would be a name another id already has.
func packageOn(logger *slog.Logger, id, content string) (string, bool) {
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		logger.Warn("a definition isn't YAML", "id", id, "error", err.Error())
		return "", false
	}
	if cfg.PackageInfo == nil || cfg.Name == "" {
		logger.Warn("a definition doesn't name its package", "id", id)
		return "", false
	}
	return cfg.Name, true
}

// ID is the id the package is kept under, and false for a package the registry doesn't
// hold.
func (i *Identities) ID(pkgName string) (string, bool) {
	if i == nil {
		return "", false
	}
	id, ok := i.byName[pkgName]
	return id, ok
}

// Package is the package kept under the id, and false when no definition says it is one.
func (i *Identities) Package(id string) (string, bool) {
	if i == nil {
		return "", false
	}
	pkgName, ok := i.byID[id]
	return pkgName, ok
}

// Mint gives a package the registry doesn't hold the id it will be kept under, and records
// it so that the next one steps past it.
func (i *Identities) Mint(pkgName string) string {
	id := MintID(i.now(), i.taken)
	i.taken[id] = struct{}{}
	i.byName[pkgName] = id
	i.byID[id] = pkgName
	return id
}

// Dir is the directory holding the package, and false when the registry holds none.
func (i *Identities) Dir(pkgName string) (string, bool) {
	id, ok := i.ID(pkgName)
	if !ok {
		return "", false
	}
	return PackageDir(id), true
}

// HeadBranch is the branch a pull request for the package is opened from.
func (i *Identities) HeadBranch(pkgName string) (string, bool) {
	id, ok := i.ID(pkgName)
	if !ok {
		return "", false
	}
	return HeadBranchName(id), true
}

// VersionHeadBranch is the branch a pull request for one version alone is opened from.
func (i *Identities) VersionHeadBranch(pkgName, version string) (string, bool) {
	id, ok := i.ID(pkgName)
	if !ok {
		return "", false
	}
	return VersionHeadBranchName(id, version), true
}

// RemoveBranch is the branch the pull request that stops serving the package is opened
// from.
func (i *Identities) RemoveBranch(pkgName string) (string, bool) {
	id, ok := i.ID(pkgName)
	if !ok {
		return "", false
	}
	return RemoveBranchName(id), true
}

// IsVersionHeadBranch reports whether the branch is one of the package's version branches.
func (i *Identities) IsVersionHeadBranch(pkgName, branch string) bool {
	id, ok := i.ID(pkgName)
	if !ok {
		return false
	}
	return IsVersionHeadBranch(id, branch)
}

// add records the packages the definitions name, in id order so that two definitions
// naming one package are resolved the same way every time.
func (i *Identities) add(logger *slog.Logger, defs map[string]string) {
	for _, id := range slices.Sorted(maps.Keys(defs)) {
		i.taken[id] = struct{}{}
		if defs[id] == "" {
			continue
		}
		pkgName, ok := packageOn(logger, id, defs[id])
		if !ok {
			continue
		}
		if held, ok := i.byName[pkgName]; ok {
			if held != id {
				logger.Warn("two ids say they hold the same package",
					"package", pkgName, "id", held, "other_id", id)
			}
			continue
		}
		i.byName[pkgName] = id
		i.byID[id] = pkgName
	}
}
