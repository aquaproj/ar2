package test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/config"
	"github.com/aquaproj/aqua/v2/pkg/config/aqua"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	aquaresolve "github.com/aquaproj/aqua/v2/pkg/resolve"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/verify"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

var errNotTemplate = errors.New("the definition doesn't declare the package served as a template")

// ChecksumChecker compares a downloaded asset with what the checksum file a definition
// names lists for it.
type ChecksumChecker interface {
	CheckListedChecksum(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset, checksumURL, assetName string, cfg *aquaregistry.Checksum) error
}

// RunTemplate checks a package served as a template by installing the versions its
// definition lists for testing: each is expanded the way aqua expands it, and every entry
// of the environment is downloaded, opened, checked for the files it names and the
// signatures it claims, and held to the checksum file the definition names when it names
// one.
//
// Nothing was generated for such a package, so these versions -- the newest and one at
// each boundary of its version_overrides -- are what stands between a definition and what
// aqua installs, as a pkg.yaml does in aqua-registry. A package that says why it can't be
// installed in CI is only reported.
func (c *Controller) RunTemplate(ctx context.Context, logger *slog.Logger, cfg *aquag2.Config, checksums ChecksumChecker) error {
	if cfg == nil || cfg.Template == nil {
		return errNotTemplate
	}
	if len(cfg.Template.TestVersions) == 0 {
		logger.Info("the template is installed by no test", "skip_test", strings.TrimSpace(cfg.Template.SkipTest))
		return nil
	}
	failed := false
	for _, version := range cfg.Template.TestVersions {
		logger := logger.With("package_version", version)
		if err := c.template(ctx, logger, cfg, version, checksums); err != nil {
			slogerr.WithError(logger, err).Error("check the template")
			failed = true
		}
	}
	if failed {
		return errFailed
	}
	return nil
}

// TemplateEnvironments lists the environments a template's test versions expand to,
// sorted, without repeats, so that the jobs are worked out from the definition.
func TemplateEnvironments(logger *slog.Logger, cfg *aquag2.Config) ([]string, error) {
	if cfg == nil || cfg.Template == nil {
		return nil, errNotTemplate
	}
	seen := map[string]struct{}{}
	for _, version := range cfg.Template.TestVersions {
		reg, _, err := expandTemplate(logger, cfg, version)
		if err != nil {
			return nil, err
		}
		for _, asset := range reg.Assets {
			seen[Environment{OS: asset.OS, Arch: asset.Arch}.String()] = struct{}{}
		}
	}
	envs := make([]string, 0, len(seen))
	for env := range seen {
		envs = append(envs, env)
	}
	slices.Sort(envs)
	return envs, nil
}

// expandTemplate resolves one version of a template for every environment it supports,
// the way aqua does: version_overrides applied as aqua-registry-g2 means them, then aqua's
// own resolution.
func expandTemplate(logger *slog.Logger, cfg *aquag2.Config, version string) (*generate.Registry, *aquaregistry.PackageInfo, error) {
	versioned, err := cfg.Versioned(logger, version)
	if err != nil {
		return nil, nil, fmt.Errorf("apply version_overrides: %w", err)
	}
	pkgs, err := aquaresolve.Resolve(logger, &aquaresolve.Param{
		PkgName: cfg.Name,
		Version: version,
		PkgInfo: versioned,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("expand the template: %w", err)
	}
	return aquag2.NewRegistry(pkgs), versioned, nil
}

func (c *Controller) template(ctx context.Context, logger *slog.Logger, cfg *aquag2.Config, version string, checksums ChecksumChecker) error {
	reg, versioned, err := expandTemplate(logger, cfg, version)
	if err != nil {
		return err
	}
	failed := false
	checked := 0
	for _, asset := range reg.Assets {
		if !c.env.matches(asset) {
			continue
		}
		checked++
		logger := logger.With("asset_env", asset.OS+"/"+asset.Arch)
		if err := c.templateAsset(ctx, logger, cfg.Name, version, asset, versioned, checksums); err != nil {
			slogerr.WithError(logger, err).Error("the template doesn't install")
			failed = true
		}
	}
	if failed {
		return errFailed
	}
	if checked == 0 {
		logger.Info("the template has nothing for this environment", "environment", c.env)
		return nil
	}
	logger.Info("the template installs", "assets", checked)
	return nil
}

func (c *Controller) templateAsset(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, versioned *aquaregistry.PackageInfo, checksums ChecksumChecker) error {
	if !downloadable(asset.Type) {
		logger.Debug("nothing to download for this type", "type", asset.Type)
		return nil
	}
	if !verify.Extractable(asset.Format) {
		logger.Info("this machine can't open the format, so the entry goes unchecked here", "format", asset.Format)
		return nil
	}
	result, err := c.verifier.Verify(ctx, logger, pkgName, version, asset)
	if err != nil {
		return fmt.Errorf("download and open the asset: %w", err)
	}
	if len(result.Unresolved) > 0 {
		return fmt.Errorf("%w: %s", errFilesMissing, strings.Join(result.Unresolved, ", "))
	}
	if result.NeedsReview {
		return errFilesMoved
	}
	if checksums == nil {
		return nil
	}
	return checkTemplateChecksum(ctx, logger, pkgName, version, asset, versioned, checksums)
}

// checksumFileURL is where the checksum file the definition names is downloaded from, or ""
// for a type whose file this can't find from the definition alone.
func checksumFileURL(pkg *config.Package, rt *runtime.Runtime) (string, error) {
	info := pkg.PackageInfo
	switch info.Checksum.Type {
	case aquaregistry.PkgInfoTypeHTTP:
		u, err := pkg.RenderChecksumURL(rt)
		if err != nil {
			return "", fmt.Errorf("render the checksum file's URL: %w", err)
		}
		return u, nil
	case aquaregistry.PkgInfoTypeGitHubRelease:
		name, err := pkg.RenderChecksumFileName(rt)
		if err != nil {
			return "", fmt.Errorf("render the checksum file's name: %w", err)
		}
		return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
			info.RepoOwner, info.RepoName, pkg.Package.Version, name), nil
	default:
		return "", nil
	}
}

// checkTemplateChecksum holds the asset to the checksum file the definition names, for the
// types whose file can be found from the definition alone.
func checkTemplateChecksum(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, versioned *aquaregistry.PackageInfo, checksums ChecksumChecker) error {
	rt := &runtime.Runtime{GOOS: asset.OS, GOARCH: asset.Arch, LibC: asset.Variants["libc"]}
	info, err := versioned.Override(logger, version, rt)
	if err != nil {
		return fmt.Errorf("apply the overrides of the environment: %w", err)
	}
	if info.Checksum == nil || !info.Checksum.GetEnabled() {
		return nil
	}
	pkg := &config.Package{Package: &aqua.Package{Name: pkgName, Version: version}, PackageInfo: info}
	checksumURL, err := checksumFileURL(pkg, rt)
	if err != nil {
		return err
	}
	if checksumURL == "" {
		logger.Info("the checksum file isn't one this can find, so it goes unchecked", "checksum_type", info.Checksum.Type)
		return nil
	}
	assetName, err := pkg.RenderAsset(rt)
	if err != nil || assetName == "" {
		assetName = path.Base(asset.URL)
	}
	return checksums.CheckListedChecksum(ctx, logger, version, asset, checksumURL, assetName, info.Checksum) //nolint:wrapcheck
}
