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

// applyAssetRules gives the environments of each rule the asset of the release its filter
// and the inference pick.
//
// A rule is for an asset the inference can't read off the release: a name that says no
// OS, such as koalaman/shellcheck's shellcheck-v0.11.0.zip for Windows. The filter only has
// to narrow the release's assets down; which of what is left is the environment's is the
// inference's, read the way aqua gr reads every asset, with the rule saying the OS and the
// architecture where the name doesn't. A filter then needn't change when a release adds an
// asset beside the one it is for. What is picked is the asset's own name, written into an
// override as it is, so nothing about it is rendered: no replacement and no extension is
// applied to it.
//
// A rule leaving nothing for an environment says nothing about that version, and the
// inference answers as it would have.
func applyAssetRules(logger *slog.Logger, p *aquaregistry.PackageInfo, rules []*g2.AssetRule, names map[string]struct{}, version string) error {
	sorted := slices.Sorted(maps.Keys(names))
	for _, rule := range rules {
		candidates, err := filterAssets(rule, sorted)
		if err != nil {
			return err
		}
		for _, env := range rule.Envs {
			goos, goarch, ok := strings.Cut(env, "/")
			if !ok {
				return fmt.Errorf("%w: %s", errAssetRuleEnv, env)
			}
			asset, info := pickAsset(candidates, version, goos, goarch)
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

// filterAssets returns the assets the rule's filter matches.
func filterAssets(rule *g2.AssetRule, names []string) ([]string, error) {
	prog, err := expr.CompileAssetFilter(rule.Filter)
	if err != nil {
		return nil, fmt.Errorf("compile the asset rule %q: %w", rule.Filter, err)
	}
	var matched []string
	for _, name := range names {
		ok, err := expr.EvaluateAssetFilter(prog, name)
		if err != nil {
			return nil, fmt.Errorf("evaluate the asset rule %q against %s: %w", rule.Filter, name, err)
		}
		if ok {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// formatRaw is the format of an asset that isn't an archive.
const formatRaw = "raw"

// noArchScore is the score aqua's parser gives an asset whose name says no architecture.
const noArchScore = -2

// pickAsset returns the asset the inference picks for the environment among the
// candidates, and how it was read. A name saying no OS is for the rule's OS, and one saying
// no architecture is for the environment's.
func pickAsset(candidates []string, version, goos, goarch string) (string, *aquaasset.AssetInfo) {
	infos := make([]*aquaasset.AssetInfo, 0, len(candidates))
	nameOf := make(map[*aquaasset.AssetInfo]string, len(candidates))
	for _, name := range candidates {
		info := aquaasset.ParseAssetName(name, version)
		if info.OS == "" {
			info.OS = goos
		}
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
