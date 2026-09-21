package generate

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config"
	aquaaqua "github.com/aquaproj/aqua/v2/pkg/config/aqua"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
)

// resolve turns the inferred PackageInfo into one Asset per supported environment.
//
// Each environment is resolved through aqua's own Override and RenderAsset, so the
// static result matches what aqua would compute at install time.
func resolve(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, version string, digests map[string]string) (*Registry, error) {
	reg := &Registry{}
	for _, rt := range expandByVariants(pkgInfo, baseRuntimes()) {
		asset, err := resolveOne(logger, pkgInfo, version, rt, digests)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			continue
		}
		reg.Assets = append(reg.Assets, asset)
	}
	if len(reg.Assets) == 0 {
		return nil, errNoSupportedEnv
	}
	return reg, nil
}

// resolveOne resolves a single environment. It returns nil when the package doesn't
// support the environment or resolves to no asset there.
func resolveOne(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, version string, rt *aquaruntime.Runtime, digests map[string]string) (*Asset, error) {
	if !pkgInfo.CheckSupportedEnvs(rt.GOOS, rt.GOARCH, rt.Env()) {
		return nil, nil //nolint:nilnil
	}
	// Copy first: SetVersion returns the receiver itself when the package has no
	// top-level version_constraint, and OverrideByRuntime then mutates it in place.
	// Without the copy, an override applied for one environment leaks into every
	// environment resolved afterwards.
	info, err := pkgInfo.Copy().Override(logger, version, rt)
	if err != nil {
		return nil, fmt.Errorf("resolve the package for %s: %w", rt.Env(), err)
	}
	pkg := &aquaconfig.Package{
		Package:     &aquaaqua.Package{Version: version},
		PackageInfo: info,
	}
	assetName, err := pkg.RenderAsset(rt)
	if err != nil {
		return nil, fmt.Errorf("render the asset name for %s: %w", rt.Env(), err)
	}
	var url string
	if info.Type == aquaregistry.PkgInfoTypeHTTP {
		url, err = pkg.RenderURL(rt)
		if err != nil {
			return nil, fmt.Errorf("render the URL for %s: %w", rt.Env(), err)
		}
	}
	if assetName == "" && url == "" {
		return nil, nil //nolint:nilnil
	}

	files, err := renderFiles(pkg, info, rt)
	if err != nil {
		return nil, err
	}

	a := &Asset{
		OS:        rt.GOOS,
		Arch:      rt.GOARCH,
		Variants:  variantsOf(rt),
		Type:      info.Type,
		RepoOwner: info.RepoOwner,
		RepoName:  info.RepoName,
		Asset:     assetName,
		URL:       url,
		Format:    info.GetFormat(),
		Files:     files,
	}
	setSigning(a, info)
	if digest, ok := digests[assetName]; ok {
		a.Checksum = digest
		a.ChecksumAlgorithm = checksumAlgorithm
	}
	return a, nil
}

// checksumAlgorithm is the algorithm of the digests GitHub reports for release assets.
const checksumAlgorithm = "sha256"

// setSigning copies the signing configuration, which is taken from aqua-registry
// because a release doesn't describe how it was signed.
func setSigning(a *Asset, info *aquaregistry.PackageInfo) {
	if info.Cosign != nil {
		a.Cosign = info.Cosign
	}
	if info.GitHubArtifactAttestations != nil {
		a.GitHubArtifactAttestations = info.GitHubArtifactAttestations
	}
}

// renderFiles resolves the templates in files[].src for the environment.
//
// aqua renders files[].src through an unexported method that supplies variables
// RenderTemplateString doesn't, such as the asset name without its extension, so
// rendering the template directly leaves "<no value>" in the result. ExePath is the
// only exported path that goes through it, so the source path is recovered by
// removing the package directory it prefixes.
func renderFiles(pkg *aquaconfig.Package, info *aquaregistry.PackageInfo, rt *aquaruntime.Runtime) ([]*File, error) {
	pkgPath, err := pkg.PkgPath(rt)
	if err != nil {
		return nil, fmt.Errorf("get the package path for %s: %w", rt.Env(), err)
	}
	infoFiles := info.GetFiles()
	files := make([]*File, 0, len(infoFiles))
	for _, f := range infoFiles {
		exePath, err := pkg.ExePath("", f, rt)
		if err != nil {
			return nil, fmt.Errorf("render files[].src for %s: %w", rt.Env(), err)
		}
		files = append(files, &File{
			Name: f.Name,
			Src:  strings.TrimPrefix(exePath, pkgPath+string(filepath.Separator)),
		})
	}
	return files, nil
}
