package generate

import (
	"fmt"
	"log/slog"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	aquaresolve "github.com/aquaproj/aqua/v2/pkg/resolve"
)

// resolve turns the inferred PackageInfo into one Asset per supported environment.
//
// The resolution is aqua's own, so that what is written here is what aqua would
// compute at install time. Doing it again in ar2 meant reproducing a set of rules
// that only aqua defines, and the two could then disagree about what a registry
// means without anything saying so.
//
// base supplies the executables. ar2 infers the asset naming from the release itself,
// which is what stops an upstream renaming from breaking the registry, but a release
// says nothing about what is inside the archive.
func resolve(logger *slog.Logger, pkgName string, pkgInfo, base *aquaregistry.PackageInfo, version string, digests map[string]string) (*Registry, error) {
	pkgs, err := aquaresolve.Resolve(logger, &aquaresolve.Param{
		PkgName:   pkgName,
		Version:   version,
		PkgInfo:   pkgInfo,
		FilesFrom: base,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve the package for each environment: %w", err)
	}

	reg := g2.NewRegistry(pkgs)
	for _, asset := range reg.Assets {
		if digest, ok := digests[asset.Asset]; ok {
			asset.Checksum = digest
			asset.ChecksumAlgorithm = checksumAlgorithm
		}
	}
	return reg, nil
}

// checksumAlgorithm is the algorithm of the digests GitHub reports for release assets.
const checksumAlgorithm = "sha256"
