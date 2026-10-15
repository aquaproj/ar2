package migrate

import (
	"slices"
	"strings"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// osNames are the operating systems an asset rule can be for, in the order the rule lists them.
var osNames = []string{"linux", "darwin", "windows"} //nolint:gochecknoglobals

// assetRulesFromTemplates is the asset rule a package whose asset names say no OS needs, or
// none.
//
// aqua-registry names an asset by a template, and a release whose assets carry no OS --
// opencontainers/runc's runc.amd64, trinodb/trino's trino-cli-483 -- is one whose template
// names none, the OS being what supported_envs says. Here the asset is read off the release by
// its name, and a name saying no OS is placed by an asset rule. So the conversion writes one:
// for the environments supported_envs names whose every template says no OS, filtered to what
// those templates match, so that the release's other assets naming no OS -- runc's source
// tarball -- aren't taken for the command.
//
// An environment whose template names its OS is the names' to place, and the rule leaves it
// out: a rule replaces what the names placed.
func assetRulesFromTemplates(pkgInfo *aquaregistry.PackageInfo) []*g2.AssetRule {
	if pkgInfo.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return nil
	}
	envs := map[string]struct{}{}
	var patterns []string
	filterable := true
	for _, e := range erasOf(pkgInfo) {
		rts, err := runtime.GetRuntimesFromEnvs(e.envs)
		if err != nil {
			continue
		}
		for _, rt := range rts {
			template := e.templateFor(rt.GOOS)
			if template == "" || namesOS(template) {
				continue
			}
			envs[rt.Env()] = struct{}{}
			pattern, ok := templatePattern(template)
			if !ok {
				// A template this can't write as an expression: the rule is left to
				// the candidates the all_assets_filter keeps, rather than to a filter
				// that matches something else.
				filterable = false
				continue
			}
			if !slices.Contains(patterns, pattern) {
				patterns = append(patterns, pattern)
			}
		}
	}
	if len(envs) == 0 {
		return nil
	}
	return []*g2.AssetRule{{Envs: sortedEnvs(envs), Filter: matchesAny(patterns, filterable)}}
}

// matchesAny is the filter matching any of the patterns, or none when one of the templates
// couldn't be written as one.
func matchesAny(patterns []string, filterable bool) string {
	if !filterable {
		return ""
	}
	conds := make([]string, 0, len(patterns))
	for _, p := range patterns {
		conds = append(conds, `Asset matches "`+p+`"`)
	}
	return strings.Join(conds, " or ")
}

// era is the part of a definition one range of versions resolves with: the top level, or a
// version_override over it.
type era struct {
	asset     string
	overrides []*aquaregistry.Override
	envs      []string
}

// erasOf is the definition's eras. A version_override says what it changes and inherits the
// rest from the top level, as aqua resolves it: runc's asset and supported_envs are both in
// its one version_override.
func erasOf(pkgInfo *aquaregistry.PackageInfo) []*era {
	out := make([]*era, 0, 1+len(pkgInfo.VersionOverrides))
	out = append(out, &era{asset: pkgInfo.Asset, overrides: pkgInfo.Overrides, envs: pkgInfo.SupportedEnvs})
	for _, vo := range pkgInfo.VersionOverrides {
		e := &era{asset: pkgInfo.Asset, overrides: pkgInfo.Overrides, envs: pkgInfo.SupportedEnvs}
		if vo.Asset != "" {
			e.asset = vo.Asset
		}
		if vo.Overrides != nil {
			e.overrides = vo.Overrides
		}
		if vo.SupportedEnvs != nil {
			e.envs = vo.SupportedEnvs
		}
		out = append(out, e)
	}
	return out
}

// templateFor is the asset template the era gives the OS: its override for the OS when that
// names an asset, and its own otherwise.
func (e *era) templateFor(goos string) string {
	for _, ov := range e.overrides {
		if ov.GOOS == goos && ov.Asset != "" {
			return ov.Asset
		}
	}
	return e.asset
}

// sortedEnvs is the environments in the order an asset rule lists them: by OS as osNames
// orders them, amd64 before arm64.
func sortedEnvs(envs map[string]struct{}) []string {
	out := make([]string, 0, len(envs))
	for _, goos := range osNames {
		for _, goarch := range []string{"amd64", "arm64"} {
			if _, ok := envs[goos+"/"+goarch]; ok {
				out = append(out, goos+"/"+goarch)
			}
		}
	}
	return out
}

// namesOS says whether an asset template names an OS: through {{.OS}}, or in a word or an
// extension the parser reads one from.
func namesOS(template string) bool {
	return strings.Contains(template, "{{.OS}}") || aquaasset.ParseAssetName(template, "").OS != ""
}
