package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	"github.com/aquaproj/aqua/v2/pkg/g2"
)

// applyAssetRules gives the environments of each rule the asset of the release picked among
// those whose name says no OS.
//
// A rule is for an asset the names can't place because its name says no OS, such as
// koalaman/shellcheck's shellcheck-v0.11.0.zip for Windows. What the rule says is that such an
// asset is for its environments: an asset naming an OS is placed by its name, so the rule
// needn't say what to leave out, and nothing about the name has to be written down for the
// rule to keep working when it changes. Its filter, when it has one, narrows the candidates
// further, for a release with more than one such asset: ip7z/7zip's 7z2604-extra.7z beside
// 7z2604-src.7z. Which of the candidates is an environment's is read off the architecture the
// name says, a name saying none being for every architecture of the rule.
//
// What a rule picks replaces whatever the names placed in the environment. A rule leaving
// nothing for an environment says nothing about that version, and the names answer as they
// would have.
func applyAssetRules(logger *slog.Logger, sel *aquaasset.Selection, rules []*g2.AssetRule, version string, spellings aquaasset.Spellings, opts *aquaasset.SelectOptions) error {
	for _, rule := range rules {
		candidates, err := filterAssets(rule.Filter, sel.Unnamed)
		if err != nil {
			return err
		}
		archs, err := archsByOS(rule.Envs)
		if err != nil {
			return err
		}
		for _, goos := range slices.Sorted(maps.Keys(archs)) {
			picked := aquaasset.SelectAs(goos, archs[goos], candidates, version, spellings, opts)
			for _, a := range picked.Assets {
				place(sel, a.OS, a.Arch, a, nil)
			}
			for _, t := range picked.Ties {
				place(sel, t.OS, t.Arch, nil, t)
			}
			for _, goarch := range archs[goos] {
				if !placed(picked, goarch) {
					logger.Info("an asset rule leaves no asset for the environment", "filter", rule.Filter, "env", goos+"/"+goarch)
				}
			}
		}
	}
	return nil
}

var errAssetRuleEnv = errors.New("an asset rule's environment isn't os/arch")

// archsByOS groups a rule's environments by OS.
func archsByOS(envs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, env := range envs {
		goos, goarch, ok := strings.Cut(env, "/")
		if !ok {
			return nil, fmt.Errorf("%w: %s", errAssetRuleEnv, env)
		}
		out[goos] = append(out[goos], goarch)
	}
	return out, nil
}

// place makes the environment's answer the asset, or the tie, a rule picked, in place of
// whatever the names placed there.
func place(sel *aquaasset.Selection, goos, goarch string, a *aquaasset.Selected, tie *aquaasset.Tie) {
	sel.Assets = slices.DeleteFunc(sel.Assets, func(s *aquaasset.Selected) bool { return s.OS == goos && s.Arch == goarch })
	sel.Ties = slices.DeleteFunc(sel.Ties, func(t *aquaasset.Tie) bool { return t.OS == goos && t.Arch == goarch })
	if a != nil {
		sel.Assets = append(sel.Assets, a)
	}
	if tie != nil {
		sel.Ties = append(sel.Ties, tie)
	}
}

func placed(sel *aquaasset.Selection, goarch string) bool {
	return slices.ContainsFunc(sel.Assets, func(s *aquaasset.Selected) bool { return s.Arch == goarch }) ||
		slices.ContainsFunc(sel.Ties, func(t *aquaasset.Tie) bool { return t.Arch == goarch })
}
