package generate

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/aqua/v2/pkg/lockfile"
	aquaresolve "github.com/aquaproj/aqua/v2/pkg/resolve"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// selectAssets resolves the release's own assets into one entry per environment.
//
// Each environment's asset is picked by its name, among the assets the definition's
// all_assets_filter keeps, and is named in the entry as the release lists it. The
// definition is not read for the asset: a template naming it breaks when the release
// renames it, and what the names say is enough to place them. What the definition still
// supplies is what a release can't say, such as the executables inside the archive and how
// the asset is signed, and an asset whose name says no OS, which its asset_rules place.
//
// A release publishing a glibc and a musl build of a platform gets an entry for each,
// distinguished by its libc variant. Which one a machine installs is aqua's to decide when
// it installs.
//
// An environment that can't be given an asset is left out, and said: one whose assets
// nothing tells apart, and one the definition's supported_envs names that the release has
// nothing for. The environments that do have an asset keep the version.
func selectAssets(logger *slog.Logger, input *Input, base *aquaregistry.PackageInfo, rel *release) (*Registry, []*Left, error) {
	def, err := definition(input.PkgName, base)
	if err != nil {
		return nil, nil, err
	}
	filter, err := allAssetsFilter(logger, input.Scaffold, def.VersionPrefix, input.Version)
	if err != nil {
		return nil, nil, err
	}
	names, err := filterAssets(filter, slices.Sorted(maps.Keys(rel.names)))
	if err != nil {
		return nil, nil, err
	}
	sel, err := pick(logger, input, def, names)
	if err != nil {
		return nil, nil, err
	}
	left, err := leftOut(sel, def)
	if err != nil {
		return nil, nil, err
	}
	reg, err := entries(logger, input, def, sel, rel.digests)
	if err != nil {
		return nil, nil, err
	}
	return reg, left, nil
}

// Left is an environment the generated file leaves out, and why.
type Left struct {
	OS   string
	Arch string
	// Libc is the libc variant the environment is for, or "".
	Libc   string
	Reason string
}

// Environment is the environment left out, as os/arch.
func (l *Left) Environment() string {
	return l.OS + "/" + l.Arch
}

// leftOut is what the file leaves out: the environments whose assets nothing tells apart,
// and the ones the definition's supported_envs names that nothing was picked for.
func leftOut(sel *aquaasset.Selection, def *aquaregistry.PackageInfo) ([]*Left, error) {
	left := make([]*Left, 0, len(sel.Ties))
	covered := map[string]struct{}{}
	for _, a := range sel.Assets {
		covered[a.Env()] = struct{}{}
	}
	for _, t := range sel.Ties {
		covered[t.OS+"/"+t.Arch] = struct{}{}
		left = append(left, &Left{
			OS: t.OS, Arch: t.Arch, Libc: t.Libc,
			Reason: fmt.Sprintf("%s: %s", errTie.Error(), strings.Join(t.Names, ", ")),
		})
	}
	if len(def.SupportedEnvs) == 0 {
		return left, nil
	}
	rts, err := runtime.GetRuntimesFromEnvs(def.SupportedEnvs)
	if err != nil {
		return nil, fmt.Errorf("read the definition's supported_envs: %w", err)
	}
	for _, rt := range rts {
		if _, ok := covered[rt.Env()]; ok {
			continue
		}
		left = append(left, &Left{OS: rt.GOOS, Arch: rt.GOARCH, Reason: errNoAssetForEnv.Error()})
	}
	return left, nil
}

// pick picks the asset of each environment. An environment whose assets nothing tells apart
// is left in the selection's ties, for the file to leave out; only a release where that is
// every environment fails.
func pick(logger *slog.Logger, input *Input, def *aquaregistry.PackageInfo, names []string) (*aquaasset.Selection, error) {
	spellings := aquaasset.Spellings(def.Replacements)
	opts := &aquaasset.SelectOptions{
		Commands: commandsOf(def),
		Format: func(goos, goarch, libc string) string {
			return declaredFormat(def, goos, goarch, libc)
		},
	}
	sel := aquaasset.Select(input.PkgName, input.Version, names, spellings, opts)
	if input.Config != nil {
		if err := applyAssetRules(logger, sel, input.Config.AssetRules, input.Version, spellings, opts); err != nil {
			return nil, err
		}
	}
	sel.Ties = definedTies(logger, sel.Ties, def)
	if len(sel.Assets) != 0 {
		for _, t := range sel.Ties {
			logger.Warn("assets nothing tells apart are left out, for a filter to settle", "tie", t.String())
		}
		return sel, nil
	}
	if len(sel.Ties) != 0 {
		ties := make([]string, len(sel.Ties))
		for i, t := range sel.Ties {
			ties[i] = t.String()
		}
		return nil, fmt.Errorf("%w: %s", errTie, strings.Join(ties, "; "))
	}
	return nil, errNoAsset
}

// definedTies returns the ties in the environments the definition says the package is for.
//
// supported_envs doesn't keep an environment the names place out of the registry: a build
// the release publishes is offered, and one that doesn't hold the package is found out when
// it is unpacked. A tie is different. It is assets nothing tells apart, and in an
// environment the definition leaves out it is usually the other programs a release
// publishes there: kcp-dev/kcp's Windows assets are kubectl plugins, not kcp.
func definedTies(logger *slog.Logger, ties []*aquaasset.Tie, def *aquaregistry.PackageInfo) []*aquaasset.Tie {
	if len(def.SupportedEnvs) == 0 {
		return ties
	}
	return slices.DeleteFunc(ties, func(t *aquaasset.Tie) bool {
		if def.CheckSupportedEnvs(t.OS, t.Arch, t.OS+"/"+t.Arch) {
			return false
		}
		logger.Info("assets nothing tells apart are left out of an environment the definition doesn't support", "tie", t.String())
		return true
	})
}

// entries resolves an entry for each picked asset, with the digest the release reports for
// it.
func entries(logger *slog.Logger, input *Input, def *aquaregistry.PackageInfo, sel *aquaasset.Selection, digests map[string]string) (*Registry, error) {
	param := &aquaresolve.Param{PkgName: input.PkgName, Version: input.Version, PkgInfo: def}
	reg := &Registry{Assets: make([]*Asset, 0, len(sel.Assets))}
	for _, a := range sel.Assets {
		pkg, err := aquaresolve.ResolveSelected(logger, param, withFormat(a, def))
		if err != nil {
			return nil, fmt.Errorf("resolve the entry of %s for %s: %w", a.Name, a.Env(), err)
		}
		reg.Assets = append(reg.Assets, g2.NewRegistry([]*lockfile.Package{pkg}).Assets...)
	}
	for _, asset := range reg.Assets {
		if digest, ok := digests[asset.Asset]; ok {
			asset.Checksum = digest
			asset.ChecksumAlgorithm = checksumAlgorithm
		}
	}
	return reg, nil
}

// commandsOf is the names of the package's executables, which tell its assets apart from
// those of the other programs its release publishes.
func commandsOf(def *aquaregistry.PackageInfo) []string {
	files := def.GetFiles()
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

// definition is the package's definition, or, for a package with none yet, what its name
// says: a GitHub release of the repository named after it.
func definition(pkgName string, base *aquaregistry.PackageInfo) (*aquaregistry.PackageInfo, error) {
	if base != nil {
		return base, nil
	}
	owner, name, err := repo(pkgName)
	if err != nil {
		return nil, err
	}
	return &aquaregistry.PackageInfo{
		Name:      pkgName,
		Type:      aquaregistry.PkgInfoTypeGitHubRelease,
		RepoOwner: owner,
		RepoName:  name,
	}, nil
}

// allAssetsFilter is the all_assets_filter answering for the version: the first entry of
// the version axis whose constraint the version meets, or the top level's when none does.
// It is aqua gr's reading of the same fields.
func allAssetsFilter(logger *slog.Logger, scaffold *genrgst.RawConfig, prefix, version string) (string, error) {
	if scaffold == nil {
		return "", nil
	}
	semver := strings.TrimPrefix(version, prefix)
	for _, vo := range scaffold.VersionOverrides {
		if vo == nil {
			continue
		}
		ok, err := expr.EvaluateVersionConstraints(logger, vo.VersionConstraint, version, semver)
		if err != nil {
			return "", fmt.Errorf("evaluate the version constraint %q: %w", vo.VersionConstraint, err)
		}
		if ok {
			return vo.AllAssetsFilter, nil
		}
	}
	return scaffold.AllAssetsFilter, nil
}

// filterAssets returns the assets an expression over Asset matches, or all of them when
// there is none.
func filterAssets(filter string, names []string) ([]string, error) {
	if filter == "" {
		return names, nil
	}
	prog, err := expr.CompileAssetFilter(filter)
	if err != nil {
		return nil, fmt.Errorf("compile the asset filter %q: %w", filter, err)
	}
	var matched []string
	for _, name := range names {
		ok, err := expr.EvaluateAssetFilter(prog, name)
		if err != nil {
			return nil, fmt.Errorf("evaluate the asset filter %q against %s: %w", filter, name, err)
		}
		if ok {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// declaredFormat is the format the definition declares for the environment, or "".
func declaredFormat(def *aquaregistry.PackageInfo, goos, goarch, libc string) string {
	p := def.Copy()
	p.OverrideByRuntime(&runtime.Runtime{GOOS: goos, GOARCH: goarch, LibC: libc})
	return p.Format
}

// withFormat is the selected asset with the format to unpack it as: the one its name ends
// in, or, for an extension the parser doesn't know, the one the definition declares for
// the environment when the name ends in that. ip7z/7zip's 7z2604-extra.7z is the latter.
func withFormat(a *aquaasset.Selected, def *aquaregistry.PackageInfo) *aquaasset.Selected {
	if a.Info.Format != "" && a.Info.Format != formatRaw {
		return a
	}
	declared := declaredFormat(def, a.OS, a.Arch, a.Libc)
	if declared == "" || declared == formatRaw || !strings.HasSuffix(a.Name, "."+declared) {
		return a
	}
	info := *a.Info
	info.Format = declared
	out := *a
	out.Info = &info
	return &out
}

// formatRaw is the format of an asset that isn't an archive.
const formatRaw = "raw"
