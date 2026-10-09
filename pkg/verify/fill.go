package verify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

// ChecksumAlgorithm is the algorithm of every checksum ar2 records.
const ChecksumAlgorithm = "sha256"

// Fill completes a registry.json.
//
// Every asset that needs a checksum and doesn't have one is downloaded and hashed.
// When extract is set, each asset is also extracted and its files are checked
// against the archive; the result says whether the outcome can be merged without
// review.
//
// Extracting is optional because it costs a download per asset even when the release
// reports digests, which is what makes a backfill bandwidth-bound. Hashing is not
// optional: a registry.json without a checksum would defeat the lock file.
func (v *Verifier) Fill(ctx context.Context, logger *slog.Logger, pkgName, version string, reg *generate.Registry, extract bool) (bool, []string, error) {
	filled, err := v.Complete(ctx, logger, pkgName, version, reg, extract)
	if err != nil {
		return false, nil, err
	}
	return filled.NeedsReview, filled.Unresolved, nil
}

// Filled is what completing a registry.json came to.
type Filled struct {
	// NeedsReview says the file can't be merged without somebody looking at it.
	NeedsReview bool
	// Unresolved names the environments whose entry points at a file the archive doesn't
	// hold, as "<os>/<arch>: <file>, <file>". They are also in Excluded; this is the
	// wording a pull request waiting for a definition is opened with.
	Unresolved []string
	// Excluded are the environments left out of the file, with the entry each had and
	// why it went. The file no longer says anything about them, so this is the only
	// record of what was tried.
	Excluded []*Excluded
	// Offered is how many environments the file is left offering. Zero is a version
	// there is nothing to publish for, and then the entries are left as they were
	// generated: what becomes of such a version is the caller's, and what it has to
	// work with is what was tried.
	Offered int
}

// Excluded is one environment the registry doesn't offer this version for.
type Excluded struct {
	// Asset is the entry as it was resolved, which is what makes the exclusion
	// checkable: it is what 'ar2 test' takes.
	Asset *generate.Asset
	// Reason is what happened, in a line.
	Reason string
}

// Environment is the environment the entry was for.
func (e *Excluded) Environment() string {
	if e.Asset == nil {
		return ""
	}
	return e.Asset.OS + "/" + e.Asset.Arch
}

// Complete is Fill, saying also which environments it left out.
//
// An environment the release doesn't publish, or publishes something the definition can't
// be read against, is left out rather than taking the version with it: the environments
// that do work get the version, and what was left out is reported so that it doesn't
// quietly stay that way. A file that offers nothing at all is not a file, so a version
// with every environment excluded is the caller's to refuse.
func (v *Verifier) Complete(ctx context.Context, logger *slog.Logger, pkgName, version string, reg *generate.Registry, extract bool) (*Filled, error) {
	filled := &Filled{}
	kept := make([]*generate.Asset, 0, len(reg.Assets))
	for _, asset := range reg.Assets {
		keep, err := v.fill(ctx, logger, pkgName, version, asset, extract, filled)
		if err != nil {
			return nil, err
		}
		if keep {
			kept = append(kept, asset)
		}
	}
	filled.Offered = len(kept)
	if len(kept) > 0 {
		reg.Assets = kept
	}
	return filled, nil
}

// fill completes one entry, and says whether the file keeps it.
//
// What loses an entry is the release: an environment it publishes nothing for, or
// publishes something the definition can't be read against. What doesn't is a server
// having a bad minute, which leaves the entry in and the version for review -- the
// difference is what Refused decides.
func (v *Verifier) fill(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, extract bool, filled *Filled) (bool, error) {
	if !fetches(asset) {
		// The entry builds the package rather than fetching one, so there is no
		// artifact: nothing to open, nothing to hash, and nothing for a person to
		// look at. Trying was reported as a failure to check the files, which left
		// every version of a cargo package waiting for a review that had nothing
		// to decide.
		logger.Debug("the entry builds the package rather than fetching it",
			"os", asset.OS, "arch", asset.Arch, "type", asset.Type)
		return true, nil
	}
	if extract && !Extractable(asset.Format) {
		// Nothing is wrong with the package; this machine has no tool to open
		// the format. The checksum below is still recorded, so the entry is
		// complete apart from its files having gone unchecked.
		logger.Warn("can't open this format here, so its files go unchecked",
			"os", asset.OS, "arch", asset.Arch, "format", asset.Format)
	} else if extract {
		if keep, done := v.extracted(ctx, logger, pkgName, version, asset, filled); done {
			return keep, nil
		}
	}
	// A checksum is not optional the way extracting is: an entry without one
	// would be installed unverified, which is what the lock file exists to stop.
	if err := v.fillChecksum(ctx, logger, version, asset); err != nil {
		if !Refused(err) {
			return false, err
		}
		// The release has no such asset. One environment it doesn't publish is not a
		// reason for the ones it does to go without the version.
		logger.Warn("the release has nothing for this environment, so the file won't offer it",
			"os", asset.OS, "arch", asset.Arch, "error", err.Error())
		filled.exclude(asset, err.Error())
		return false, nil
	}
	return true, nil
}

// extracted opens the asset and reads its files, and says whether the file keeps the entry
// and whether anything is left to do for it.
//
// Nothing here fails a version: what it meets is either the release's answer about an
// environment or a moment that may answer differently, and both are recorded rather than
// returned.
func (v *Verifier) extracted(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, filled *Filled) (bool, bool) {
	review, missing, holds, err := v.fillByExtracting(ctx, logger, pkgName, version, asset)
	switch {
	case len(missing) > 0:
		// The entry names a file the archive doesn't hold anywhere, so what it says
		// about this environment can't be true. What the archive holds instead goes
		// with it: it is the one thing that says what the definition should have said.
		reason := describe(asset, missing, holds)
		filled.Unresolved = append(filled.Unresolved, reason)
		filled.exclude(asset, reason)
		return false, true
	case err != nil && Refused(err):
		logger.Warn("the release has nothing for this environment, so the file won't offer it",
			"os", asset.OS, "arch", asset.Arch, "error", err.Error())
		filled.exclude(asset, err.Error())
		return false, true
	case err != nil:
		// The entry isn't mergeable as it stands, but it is most of the way
		// there: the checksum may already be recorded and the asset name came
		// from the release. Throwing it away would mean generating it again from
		// nothing, so it goes out for review and whoever picks it up starts from
		// the pull request rather than from scratch.
		logger.Warn("failed to check the files against the archive",
			"os", asset.OS, "arch", asset.Arch, "error", err.Error())
		filled.NeedsReview = true
		return true, false
	default:
		filled.NeedsReview = filled.NeedsReview || review
		// Extracting recorded the checksum, so there is nothing else for this entry.
		return true, true
	}
}

// exclude records an environment the file won't offer.
func (f *Filled) exclude(asset *generate.Asset, reason string) {
	f.Excluded = append(f.Excluded, &Excluded{Asset: asset, Reason: reason})
}

// fillByExtracting extracts the asset, resolves its files, and records the checksum. It
// reports whether what it resolved has to be looked at, and which files it couldn't resolve
// at all.
func (v *Verifier) fillByExtracting(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) (bool, []string, []string, error) {
	result, err := v.Verify(ctx, logger, pkgName, version, asset)
	if err != nil {
		return false, nil, nil, fmt.Errorf("verify the asset for %s/%s: %w", asset.OS, asset.Arch, err)
	}
	asset.Files = result.Files
	asset.LinkedLibc = result.LinkedLibc
	if err := setChecksum(asset, result.Checksum); err != nil {
		return false, nil, nil, err
	}
	if len(result.Unresolved) > 0 {
		logger.Warn("the archive holds no file of this name, anywhere",
			"os", asset.OS, "arch", asset.Arch,
			"unresolved", result.Unresolved, "archive_holds", result.Holds)
		return false, result.Unresolved, result.Holds, nil
	}
	if result.NeedsReview {
		logger.Warn("the files of this asset don't match the archive",
			"os", asset.OS, "arch", asset.Arch)
	}
	return result.NeedsReview, nil, nil, nil
}

// describe says what one environment couldn't resolve and what its archive holds instead.
func describe(asset *generate.Asset, missing, holds []string) string {
	out := asset.OS + "/" + asset.Arch + ": " + strings.Join(missing, ", ")
	if len(holds) == 0 {
		// An archive of nothing, which is a release that packaged nothing rather than one
		// whose layout moved.
		return out + " (the archive holds nothing)"
	}
	return out + " (the archive holds " + strings.Join(holds, ", ") + ")"
}

// fillChecksum downloads the asset only when its checksum is still missing.
func (v *Verifier) fillChecksum(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset) error {
	if asset.Checksum != "" || !fetches(asset) {
		return nil
	}
	logger.Debug("the release reports no digest; hashing the asset",
		"os", asset.OS, "arch", asset.Arch, "asset", asset.Asset)
	checksum, err := v.Checksum(ctx, logger, version, asset)
	if err != nil {
		return fmt.Errorf("hash the asset for %s/%s: %w", asset.OS, asset.Arch, err)
	}
	return setChecksum(asset, checksum)
}

// setChecksum records the checksum, or reports a digest that disagrees with the
// bytes. Disagreeing means the asset was replaced after it was published, which is
// the case a checksum exists to catch.
func setChecksum(asset *generate.Asset, checksum string) error {
	if asset.Checksum == "" {
		asset.Checksum = checksum
		asset.ChecksumAlgorithm = ChecksumAlgorithm
		return nil
	}
	if asset.Checksum != checksum {
		return fmt.Errorf("the digest of %s doesn't match the downloaded asset", asset.Asset)
	}
	return nil
}

// fetches reports whether the entry installs by downloading an artifact.
//
// go_install and cargo build from source through another tool, which does its own
// verification, so there is nothing for ar2 to open and nothing to hash. Every other type
// downloads something, and that something is what is checked.
func fetches(asset *generate.Asset) bool {
	switch asset.Type {
	case aquaregistry.PkgInfoTypeGoInstall, aquaregistry.PkgInfoTypeCargo:
		return false
	}
	return true
}
