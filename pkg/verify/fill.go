package verify

import (
	"context"
	"fmt"
	"log/slog"

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
func (v *Verifier) Fill(ctx context.Context, logger *slog.Logger, pkgName, version string, reg *generate.Registry, extract bool) (bool, error) {
	needsReview := false
	for _, asset := range reg.Assets {
		if extract && !Extractable(asset.Format) {
			// Nothing is wrong with the package; this machine has no tool to open
			// the format. The checksum below is still recorded, so the entry is
			// complete apart from its files having gone unchecked.
			logger.Warn("can't open this format here, so its files go unchecked",
				"os", asset.OS, "arch", asset.Arch, "format", asset.Format)
		} else if extract {
			review, err := v.fillByExtracting(ctx, logger, pkgName, version, asset)
			if err == nil {
				needsReview = needsReview || review
				continue
			}
			// The entry isn't mergeable as it stands, but it is most of the way
			// there: the checksum may already be recorded and the asset name came
			// from the release. Throwing it away would mean generating it again from
			// nothing, so it goes out for review and whoever picks it up starts from
			// the pull request rather than from scratch.
			logger.Warn("failed to check the files against the archive",
				"os", asset.OS, "arch", asset.Arch, "error", err.Error())
			needsReview = true
		}
		// A checksum is not optional the way extracting is: an entry without one
		// would be installed unverified, which is what the lock file exists to stop.
		if err := v.fillChecksum(ctx, logger, version, asset); err != nil {
			return false, err
		}
	}
	return needsReview, nil
}

// fillByExtracting extracts the asset, resolves its files, and records the checksum.
func (v *Verifier) fillByExtracting(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) (bool, error) {
	result, err := v.Verify(ctx, logger, pkgName, version, asset)
	if err != nil {
		return false, fmt.Errorf("verify the asset for %s/%s: %w", asset.OS, asset.Arch, err)
	}
	asset.Files = result.Files
	asset.LinkedLibc = result.LinkedLibc
	if err := setChecksum(asset, result.Checksum); err != nil {
		return false, err
	}
	if result.NeedsReview {
		logger.Warn("the files of this asset don't match the archive",
			"os", asset.OS, "arch", asset.Arch, "unresolved", result.Unresolved)
	}
	return result.NeedsReview, nil
}

// fillChecksum downloads the asset only when its checksum is still missing.
func (v *Verifier) fillChecksum(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset) error {
	if asset.Checksum != "" || !needsChecksum(asset) {
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

// needsChecksum reports whether the package type has bytes to checksum.
//
// go_install and cargo build from source through another tool, which does its own
// verification, so there is no artifact for ar2 to hash. Every other type downloads
// something, and that something must be checksummed.
func needsChecksum(asset *generate.Asset) bool {
	switch asset.Type {
	case aquaregistry.PkgInfoTypeGoInstall, aquaregistry.PkgInfoTypeCargo:
		return false
	}
	return true
}
