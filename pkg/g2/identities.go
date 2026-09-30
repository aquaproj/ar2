package g2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// Identities is what each package's branch is named after.
//
// A branch named after an id doesn't say which package it holds, so this is read from the
// branches themselves: the branch name carries the id and the definition on the branch
// carries the package's name. Every branch answers for itself, where a table kept anywhere
// else -- the catalogue, say -- could disagree with what the branches hold and send a
// package's versions to another package's branch.
//
// A package the table doesn't have is one the registry doesn't hold yet. Taking it over
// mints its id, and the next reading of the branches finds it there.
type Identities struct {
	// byName is the package's name to the id of the branch holding it.
	byName map[string]string
	// taken is every id a branch is named after, including the branches whose
	// definition couldn't be read. An id is minted by stepping past what is taken, so
	// leaving one out here would hand out an id a branch already has.
	taken map[string]struct{}
	// byID is the other way round, for a caller that has a branch and wants the package
	// it holds.
	byID map[string]string
	// now is what an id is minted from. A field so that a test can mint a known one.
	now func() time.Time
}

// errNoIdentity says the registry holds no branch for the package, so there is nothing to
// write to. A package is taken over by creating its branch, which is what mints its id.
var errNoIdentity = errors.New("the registry holds no branch for the package")

// errNoIdentities says the client was never told what the branches are named after, so it
// has nowhere to put a package. It is a wiring mistake rather than something about the
// registry, which is why creating a branch says it instead of minting an id into the dark.
var errNoIdentities = errors.New("the client wasn't told what the package branches are named after")

// Definitions is the definition on every package branch, read in one pass.
//
// One query per hundred branches rather than a request per package, which is what makes
// reading the whole table affordable at the start of a run.
type Definitions interface {
	Files(ctx context.Context, logger *slog.Logger, prefix, path string) (map[string]string, error)
}

// ReadIdentities reads what every package's branch is named after, and returns the
// definition each branch holds along with it.
//
// The definitions come back because they are what the table was read out of: a caller that
// works through every definition -- the tidy, the reconciliation -- would otherwise read
// the same hundred-branch pages again to get them.
func ReadIdentities(ctx context.Context, logger *slog.Logger, defs Definitions) (*Identities, map[string]string, error) {
	files, err := defs.Files(ctx, logger, BranchPrefix, ConfigFileName)
	if err != nil {
		return nil, nil, fmt.Errorf("read the definition on every package branch: %w", err)
	}
	return NewIdentities(logger, files), files, nil
}

// NewIdentities reads the table out of the definitions the package branches hold, keyed by
// branch name.
//
// A branch still named after the package is not in it. Those are the ones 'ar2 identify'
// carries over, and addressing one by the name it happens to have would write to a branch
// nothing reads.
func NewIdentities(logger *slog.Logger, files map[string]string) *Identities {
	ids := &Identities{
		byName: make(map[string]string, len(files)),
		byID:   make(map[string]string, len(files)),
		taken:  make(map[string]struct{}, len(files)),
		now:    time.Now,
	}
	// In branch order, so that two branches claiming one package are resolved the same
	// way every time rather than by the order a listing came back in.
	for _, branch := range slices.Sorted(maps.Keys(files)) {
		id, ok := BranchID(branch)
		if !ok {
			continue
		}
		ids.taken[id] = struct{}{}
		pkgName, ok := packageOn(logger, branch, files[branch])
		if !ok {
			continue
		}
		if held, ok := ids.byName[pkgName]; ok {
			logger.Warn("two branches say they hold the same package",
				"package", pkgName, "branch", IDBranchName(held), "other_branch", branch)
			continue
		}
		ids.byName[pkgName] = id
		ids.byID[id] = pkgName
	}
	return ids
}

// packageOn is the package a branch says it holds.
//
// The definition is the only thing that says it once the branch name is an id, so a
// definition that doesn't name a package leaves the branch unaddressable. That is reported
// rather than guessed at: the guess would be a name already taken by another branch.
func packageOn(logger *slog.Logger, branch, content string) (string, bool) {
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		logger.Warn("a package branch holds a definition that isn't YAML",
			"branch", branch, "error", err.Error())
		return "", false
	}
	if cfg.PackageInfo == nil || cfg.Name == "" {
		logger.Warn("a package branch holds a definition that doesn't name its package",
			"branch", branch)
		return "", false
	}
	return cfg.Name, true
}

// ID is the id the package's branch is named after, and false for a package the registry
// doesn't hold.
func (i *Identities) ID(pkgName string) (string, bool) {
	if i == nil {
		return "", false
	}
	id, ok := i.byName[pkgName]
	return id, ok
}

// Package is the package the branch whose id this is holds, and false when no branch says
// it holds one.
func (i *Identities) Package(id string) (string, bool) {
	if i == nil {
		return "", false
	}
	pkgName, ok := i.byID[id]
	return pkgName, ok
}

// Mint gives a package the registry doesn't hold the id its branch will be named after,
// and records it so that the next one steps past it.
func (i *Identities) Mint(pkgName string) string {
	id := MintID(i.now(), i.taken)
	i.taken[id] = struct{}{}
	i.byName[pkgName] = id
	i.byID[id] = pkgName
	return id
}

// Branch is the branch holding the package, and false when the registry holds none.
func (i *Identities) Branch(pkgName string) (string, bool) {
	id, ok := i.ID(pkgName)
	if !ok {
		return "", false
	}
	return IDBranchName(id), true
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
