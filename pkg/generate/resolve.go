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
	reg.Assets = definedEnvs(reg.Assets, pkgInfo, base)
	for _, asset := range reg.Assets {
		if digest, ok := digests[asset.Asset]; ok {
			asset.Checksum = digest
			asset.ChecksumAlgorithm = checksumAlgorithm
		}
	}
	return reg, nil
}

// definedEnvs keeps the entries for the environments the definition says the package is
// for.
//
// The inference says which environments the release publishes an asset for, and a release
// can publish one that doesn't hold the package: kcp-dev/kcp's Windows archives hold only
// its kubectl plugins. supported_envs is how a definition says so, and an environment it
// leaves out is one the package isn't for, not one generating failed for.
//
// An arm64 machine running the amd64 build is read the way aqua reads it, from whether the
// package runs under Rosetta 2 or Windows' emulation, which is usually the inference's to say:
// a definition naming amd64 means those too.
func definedEnvs(assets []*g2.Asset, pkgInfo, base *aquaregistry.PackageInfo) []*g2.Asset {
	if base == nil || len(base.SupportedEnvs) == 0 {
		return assets
	}
	envs := &aquaregistry.PackageInfo{
		SupportedEnvs:       base.SupportedEnvs,
		Rosetta2:            base.Rosetta2 || pkgInfo.Rosetta2,
		WindowsARMEmulation: base.WindowsARMEmulation || pkgInfo.WindowsARMEmulation,
	}
	kept := make([]*g2.Asset, 0, len(assets))
	for _, asset := range assets {
		if envs.CheckSupportedEnvs(asset.OS, asset.Arch, asset.OS+"/"+asset.Arch) {
			kept = append(kept, asset)
		}
	}
	return kept
}

// checksumAlgorithm is the algorithm of the digests GitHub reports for release assets.
const checksumAlgorithm = "sha256"
