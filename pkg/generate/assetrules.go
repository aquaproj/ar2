package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/aqua/v2/pkg/g2"
)

// assetRelease is what an asset rule is applied to: the release of one version, and what
// the definition says about reading its assets.
type assetRelease struct {
	names   map[string]struct{}
	version string
	// filter is the all_assets_filter that answers for the version, or "".
	filter    string
	spellings aquaasset.Spellings
}

// newAssetRelease is the release of the version, read the way the inference read it:
// through the same all_assets_filter and the same spellings.
func newAssetRelease(logger *slog.Logger, cfg *g2.Config, base *aquaregistry.PackageInfo, version string, names map[string]struct{}) *assetRelease {
	rel := &assetRelease{names: names, version: version}
	prefix := ""
	if base != nil {
		prefix = base.VersionPrefix
		rel.spellings = aquaasset.Spellings(base.Replacements)
	}
	rel.filter = assetFilterFor(logger, cfg, prefix, version)
	return rel
}

// applyAssetRules gives the environments of each rule the asset of the release the
// inference picks among those whose name says no OS.
//
// A rule is for an asset the inference can't read off the release because its name says
// no OS, such as koalaman/shellcheck's shellcheck-v0.11.0.zip for Windows. What the rule
// says is that such an asset is for its environments: an asset naming an OS is the
// inference's to place, so the rule needn't say what to leave out, and nothing about the
// name has to be written down for the rule to keep working when it changes. Its filter,
// when it has one, narrows the candidates further, for a release with more than one such
// asset: ip7z/7zip's 7z2604-extra.7z beside 7z2604-src.7z. Which of the candidates is an
// environment's is the inference's again, by the architecture the name says, a name saying
// none being for every architecture of the rule.
//
// What is picked is the asset's own name, written into an override as it is, so nothing
// about it is rendered: no replacement and no extension is applied to it. A rule leaving
// nothing for an environment says nothing about that version, and the inference answers
// as it would have.
func applyAssetRules(logger *slog.Logger, p *aquaregistry.PackageInfo, rules []*g2.AssetRule, rel *assetRelease) error {
	unnamed, err := unnamedOS(rel)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		candidates, err := filterAssets(rule.Filter, unnamed)
		if err != nil {
			return err
		}
		for _, env := range rule.Envs {
			goos, goarch, ok := strings.Cut(env, "/")
			if !ok {
				return fmt.Errorf("%w: %s", errAssetRuleEnv, env)
			}
			asset, info := pickAsset(candidates, rel, goos, goarch)
			if info == nil {
				logger.Info("an asset rule leaves no asset for the environment", "filter", rule.Filter, "env", env)
				continue
			}
			giveAsset(p, goos, goarch, asset, formatOf(p, goos, goarch, asset, info.Format))
		}
	}
	return nil
}

var errAssetRuleEnv = errors.New("an asset rule's environment isn't os/arch")

// unnamedOS returns the assets of the release that the all_assets_filter keeps and whose
// name the inference reads no OS from, sorted.
func unnamedOS(rel *assetRelease) ([]string, error) {
	kept, err := filterAssets(rel.filter, slices.Sorted(maps.Keys(rel.names)))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(kept))
	for _, name := range kept {
		if aquaasset.ParseAssetName(name, rel.version, rel.spellings).OS == "" {
			out = append(out, name)
		}
	}
	return out, nil
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

// assetFilterFor is the all_assets_filter answering for the version: the first entry of
// asset_filters whose constraint the version meets, or the top level's when none does.
// It is aqua gr's reading of the same fields, which the inference has filtered by.
func assetFilterFor(logger *slog.Logger, cfg *g2.Config, prefix, version string) string {
	semver := strings.TrimPrefix(version, prefix)
	for _, f := range cfg.AssetFilters {
		if f == nil {
			continue
		}
		ok, err := expr.EvaluateVersionConstraints(logger, f.VersionConstraint, version, semver)
		if err == nil && ok {
			return f.AllAssetsFilter
		}
	}
	return cfg.AllAssetsFilter
}

// formatRaw is the format of an asset that isn't an archive.
const formatRaw = "raw"

// noArchScore is the score aqua's parser gives an asset whose name says no architecture.
const noArchScore = -2

// pickAsset returns the asset the inference picks for the environment among the
// candidates, and how it was read. The candidates say no OS, so each is read as the
// environment's, and one saying no architecture is for the environment's too.
func pickAsset(candidates []string, rel *assetRelease, goos, goarch string) (string, *aquaasset.AssetInfo) {
	infos := make([]*aquaasset.AssetInfo, 0, len(candidates))
	nameOf := make(map[*aquaasset.AssetInfo]string, len(candidates))
	for _, name := range candidates {
		info := aquaasset.ParseAssetName(name, rel.version, rel.spellings)
		info.OS = goos
		if info.Score == noArchScore {
			info.Arch = goarch
		}
		infos = append(infos, info)
		nameOf[info] = name
	}
	info := aquaasset.GetOSArch(goos, goarch, infos)
	if info == nil {
		return "", nil
	}
	return nameOf[info], info
}

// formatOf is the format to unpack the asset as: the one its name ends in, or, for an
// extension the inference doesn't know, the one the definition declares for the
// environment when the name ends in that. ip7z/7zip's 7z2604-extra.7z is the latter.
func formatOf(p *aquaregistry.PackageInfo, goos, goarch, asset, parsed string) string {
	if parsed != "" && parsed != formatRaw {
		return parsed
	}
	declared := declaredFormat(p, goos, goarch)
	if declared != "" && strings.HasSuffix(asset, "."+declared) {
		return declared
	}
	return formatRaw
}

// declaredFormat is the format the first override applying to the environment declares,
// or "" when it declares none.
func declaredFormat(p *aquaregistry.PackageInfo, goos, goarch string) string {
	for _, ov := range p.Overrides {
		if len(ov.Variants) != 0 {
			continue
		}
		if (ov.GOOS == "" || ov.GOOS == goos) && (ov.GOArch == "" || ov.GOArch == goarch) {
			return ov.Format
		}
	}
	return ""
}

// giveAsset makes the environment install the asset, ahead of what the inference says
// for it but after the overrides selecting on a variant, which aqua applies first.
func giveAsset(p *aquaregistry.PackageInfo, goos, goarch, asset, format string) {
	no := false
	p.Overrides = slices.Insert(p.Overrides, leadingVariants(p.Overrides), &aquaregistry.Override{
		GOOS:               goos,
		GOArch:             goarch,
		Asset:              asset,
		Format:             format,
		CompleteWindowsExt: &no,
		AppendExt:          &no,
	})
	env := goos + "/" + goarch
	if p.SupportedEnvs != nil && !p.CheckSupportedEnvs(goos, goarch, env) {
		p.SupportedEnvs = append(slices.Clone(p.SupportedEnvs), env)
	}
}

// leadingVariants is how many overrides at the start of the list select on a variant.
func leadingVariants(overrides []*aquaregistry.Override) int {
	for i, ov := range overrides {
		if len(ov.Variants) == 0 {
			return i
		}
	}
	return len(overrides)
}
