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
	base, err := resolveBase(logger, input)
	if err != nil {
		return nil, err
	}

	// Only a GitHub release exposes an asset list to infer from. For every other
	// type the URL or the path is a template that nothing but registry.yaml knows,
	// so its definition is used as it is.
	if base != nil && base.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return resolve(logger, base, nil, input.Version, nil)
	}

	inferred, err := g.packageInfo(ctx, logger, input)
	if err != nil {
		return nil, err
	}
	digests, err := g.digests(ctx, input)
	if err != nil {
		return nil, err
	}
	return resolve(logger, merge(inferred, base), base, input.Version, digests)
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

// digests returns the SHA256 digest of each asset, keyed by asset name.
//
// GitHub started exposing digests on 2025-06-03 and doesn't backfill them, so this
// is empty for older releases. Those need the asset downloaded and hashed, which
// this doesn't do yet.
func (g *Generator) digests(ctx context.Context, input *Input) (map[string]string, error) {
	owner, name, found := strings.Cut(input.PkgName, "/")
	if !found {
		return nil, errPkgNameFormat
	}
	// A package name can have more than two segments (a monorepo publishing several
	// binaries); the repository is the first two.
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	release, _, err := g.gh.GetReleaseByTag(ctx, owner, name, input.Version)
	if err != nil {
		return nil, fmt.Errorf("get the release: %w", err)
	}
	digests := map[string]string{}
	for _, asset := range release.Assets {
		digest := asset.GetDigest()
		if digest == "" {
			continue
		}
		digests[asset.GetName()] = strings.TrimPrefix(digest, "sha256:")
	}
	return digests, nil
}
