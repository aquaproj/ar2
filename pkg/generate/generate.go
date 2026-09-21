package generate

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// Input holds the parameters of a single generation.
type Input struct {
	// PkgName is the aqua package name, e.g. "cli/cli".
	PkgName string
	// Version is the release tag, e.g. "v2.101.0".
	Version string
	// Scaffold is the aqua gr configuration of the package, taken from
	// aqua-registry's scaffold.yaml. It supplies the filters that can't be inferred
	// from the release, such as which assets to ignore. It may be nil.
	Scaffold *genrgst.RawConfig
	// Base is the package's definition in aqua-registry, already resolved for the
	// version. It supplies what a release can't express: files, libc variants, and
	// the signing configuration. It may be nil for a package aqua-registry doesn't
	// have yet.
	Base *aquaregistry.PackageInfo
	// Config is the package's definition on its aqua-registry-g2 branch. When it is
	// set it replaces Base and Scaffold, because it is what those were converted
	// into: aqua-registry is where a package's definition comes from until
	// aqua-registry-g2 has one of its own, and after that it is no longer consulted.
	Config *g2.Config
}

// Generator builds registry.json from a release.
type Generator struct {
	gh genrgst.RepositoriesService
}

// New creates a Generator.
func New(gh genrgst.RepositoriesService) *Generator {
	return &Generator{gh: gh}
}

// Generate resolves a release into registry.json.
//
// The asset naming rule is not read from a registry: aqua gr infers it from the
// release's own asset list, which is what stops an upstream renaming from breaking
// the registry. The inferred PackageInfo is then resolved for each os/arch the same
// way aqua resolves it at install time, so the static result installs identically.
func (g *Generator) Generate(ctx context.Context, logger *slog.Logger, input *Input) (*Registry, error) {
	input, err := useConfig(logger, input)
	if err != nil {
		return nil, err
	}
	base, err := resolveBase(logger, input)
	if err != nil {
		return nil, err
	}

	// Only a GitHub release exposes an asset list to infer from. For every other
	// type the URL or the path is a template that nothing but registry.yaml knows,
	// so its definition is used as it is.
	if base != nil && base.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return resolve(logger, input.PkgName, base, nil, input.Version, nil)
	}

	inferred, err := g.packageInfo(ctx, logger, input)
	if err != nil {
		return nil, err
	}
	rel, err := g.release(ctx, input)
	if err != nil {
		return nil, err
	}
	reg, err := resolve(logger, input.PkgName, merge(inferred, base), base, input.Version, rel.digests)
	if err != nil {
		return nil, err
	}
	if err := inferSigning(input.PkgName, reg, rel.names); err != nil {
		return nil, err
	}
	return reg, nil
}

// useConfig replaces the aqua-registry definition with aqua-registry-g2's own when
// the package has one.
//
// The two say the same things; g2's is what aqua-registry's was converted into, minus
// what a release is read for. Preferring it is what makes the conversion take effect:
// the filters it carries are what decide which assets are considered at all, and a
// correction made to it would otherwise never be used.
//
// Base is already resolved for the version by resolveBase, so the definition is
// handed over in the same shape.
func useConfig(logger *slog.Logger, input *Input) (*Input, error) {
	if input.Config == nil {
		return input, nil
	}
	pkgInfo, err := input.Config.SetVersion(logger, input.Version)
	if err != nil {
		return nil, fmt.Errorf("resolve the package definition for the version: %w", err)
	}
	// SetVersion has applied the overrides, so resolveBase must not apply them
	// again.
	pkgInfo.VersionConstraints = ""
	pkgInfo.VersionOverrides = nil

	replaced := *input
	replaced.Base = pkgInfo
	replaced.Scaffold = &genrgst.RawConfig{
		AllAssetsFilter: input.Config.AllAssetsFilter,
		VersionFilter:   pkgInfo.VersionFilter,
		VersionPrefix:   pkgInfo.VersionPrefix,
	}
	return &replaced, nil
}

// resolveBase applies the version_overrides of aqua-registry's definition, so that
// what is merged is the definition for this version rather than the latest one.
func resolveBase(logger *slog.Logger, input *Input) (*aquaregistry.PackageInfo, error) {
	if input.Base == nil {
		return nil, nil //nolint:nilnil
	}
	base, err := input.Base.SetVersion(logger, input.Version)
	if err != nil {
		return nil, fmt.Errorf("apply version_overrides of the aqua-registry definition: %w", err)
	}
	return base, nil
}

// packageInfo runs aqua gr for one version and reads back the PackageInfo it prints.
//
// aqua gr only exposes its inference by writing YAML to a writer, so the result is
// parsed back rather than reimplemented. Reimplementing it would risk resolving
// differently from aqua itself, which is the one thing the generated registry.json
// must not do.
func (g *Generator) packageInfo(ctx context.Context, logger *slog.Logger, input *Input) (*aquaregistry.PackageInfo, error) {
	param := &aquaconfig.Param{
		// Limit 1 makes aqua gr resolve the single release given by "<pkg>@<version>"
		// instead of walking the release history to build version_overrides.
		Limit: 1,
	}
	if input.Scaffold != nil {
		path, clean, err := writeScaffold(input.Scaffold)
		if err != nil {
			return nil, err
		}
		defer clean()
		param.GenerateConfigFilePath = path
	}

	buf := &bytes.Buffer{}
	ctrl := genrgst.NewController(g.gh, nil, nil, buf)
	if err := ctrl.GenerateRegistry(ctx, param, logger, input.PkgName+"@"+input.Version); err != nil {
		return nil, fmt.Errorf("generate a registry: %w", err)
	}

	cfg := &aquaregistry.Config{}
	if err := yaml.NewDecoder(buf).Decode(cfg); err != nil {
		return nil, fmt.Errorf("read the generated registry as YAML: %w", err)
	}
	if len(cfg.PackageInfos) == 0 {
		return nil, errNoPackage
	}
	return cfg.PackageInfos[0], nil
}

// scaffoldFilePerm keeps the temporary scaffold file readable only by ar2.
const scaffoldFilePerm = 0o600

// writeScaffold writes the aqua gr configuration to a temporary file, which is the
// only way GenerateRegistry accepts it.
func writeScaffold(raw *genrgst.RawConfig) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ar2")
	if err != nil {
		return "", nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	clean := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, "scaffold.yaml")
	b, err := yaml.Marshal(raw)
	if err != nil {
		clean()
		return "", nil, fmt.Errorf("marshal the scaffold configuration: %w", err)
	}
	if err := os.WriteFile(path, b, scaffoldFilePerm); err != nil {
		clean()
		return "", nil, fmt.Errorf("write the scaffold configuration: %w", err)
	}
	return path, clean, nil
}

// release is what a release says about its own assets.
type release struct {
	// digests holds the SHA256 digest of each asset that has one, keyed by name.
	//
	// GitHub started exposing digests on 2025-06-03 and doesn't backfill them, so
	// this is empty for older releases. Those are downloaded and hashed instead.
	digests map[string]string
	// names holds every asset name, which is what the signatures are found by.
	names map[string]struct{}
}

// release reads the release's asset list.
//
// The list comes with the release rather than being fetched separately, which is one
// request instead of two per version. Assets whose upload never completed are left
// out: GitHub keeps them in the "starter" state, where they are hidden from the
// release page and can't be downloaded.
func (g *Generator) release(ctx context.Context, input *Input) (*release, error) {
	owner, name, err := repo(input.PkgName)
	if err != nil {
		return nil, err
	}
	rel, _, err := g.gh.GetReleaseByTag(ctx, owner, name, input.Version)
	if err != nil {
		return nil, fmt.Errorf("get the release: %w", err)
	}
	out := &release{
		digests: map[string]string{},
		names:   map[string]struct{}{},
	}
	for _, asset := range rel.Assets {
		if asset.GetState() != assetStateUploaded {
			continue
		}
		out.names[asset.GetName()] = struct{}{}
		if digest := asset.GetDigest(); digest != "" {
			out.digests[asset.GetName()] = strings.TrimPrefix(digest, "sha256:")
		}
	}
	return out, nil
}

// assetStateUploaded is the state of an asset whose upload has completed.
const assetStateUploaded = "uploaded"

// repo returns the repository a package is released from.
//
// A package name can have more than two segments — a monorepo publishing several
// binaries — and the repository is the first two.
func repo(pkgName string) (string, string, error) {
	owner, name, found := strings.Cut(pkgName, "/")
	if !found {
		return "", "", errPkgNameFormat
	}
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	return owner, name, nil
}
