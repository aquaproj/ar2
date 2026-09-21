package generate

import (
	"maps"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
)

// baseRuntimes returns the os/arch combinations aqua supports.
func baseRuntimes() []*aquaruntime.Runtime {
	oses := aquaruntime.GOOSList()
	arches := aquaruntime.GOARCHList()
	rts := make([]*aquaruntime.Runtime, 0, len(oses)*len(arches))
	for _, goos := range oses {
		for _, goarch := range arches {
			rts = append(rts, &aquaruntime.Runtime{GOOS: goos, GOARCH: goarch})
		}
	}
	return rts
}

// expandByVariants duplicates each runtime to cover every variant value the package
// declares for that os/arch.
//
// Without it, only one of a set of sibling overrides that differ solely by variants
// is ever resolved, so a package shipping separate musl and glibc builds would lose
// one of them. aqua does the same expansion in update-checksum, but that code is
// unexported, so it is done here on top of the exported helpers.
func expandByVariants(pkgInfo *aquaregistry.PackageInfo, rts []*aquaruntime.Runtime) []*aquaruntime.Runtime {
	if len(pkgInfo.Overrides) == 0 {
		return rts
	}
	keys := aquaregistry.SupportedVariantKeys()
	expanded := make([]*aquaruntime.Runtime, 0, len(rts))
	seen := map[string]struct{}{}
	add := func(rt *aquaruntime.Runtime) {
		k := rt.GOOS + "/" + rt.GOARCH + "/" + rt.LibC
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		expanded = append(expanded, rt)
	}
	for _, rt := range rts {
		candidates := platformCandidates(pkgInfo.Overrides, rt)
		if !hasVariants(candidates) {
			add(rt)
			continue
		}
		for _, combo := range cartesianProduct(variantValueSets(candidates, keys)) {
			newRT := *rt
			applyVariants(&newRT, combo)
			add(&newRT)
		}
	}
	return expanded
}

// platformCandidates returns the overrides matching rt by os/arch, ignoring variants.
// An override naming a variant key aqua doesn't evaluate is dropped, because Match
// would reject it for every runtime anyway.
func platformCandidates(overrides []*aquaregistry.Override, rt *aquaruntime.Runtime) []*aquaregistry.Override {
	out := make([]*aquaregistry.Override, 0, len(overrides))
	for _, ov := range overrides {
		if !ov.MatchPlatform(rt) {
			continue
		}
		if !variantKeysSupported(ov) {
			continue
		}
		out = append(out, ov)
	}
	return out
}

func variantKeysSupported(ov *aquaregistry.Override) bool {
	for _, v := range ov.Variants {
		if !aquaregistry.IsSupportedVariantKey(v.Key) {
			return false
		}
	}
	return true
}

func hasVariants(overrides []*aquaregistry.Override) bool {
	for _, ov := range overrides {
		if len(ov.Variants) > 0 {
			return true
		}
	}
	return false
}

// variantValueSets returns, per variant key, the values to enumerate.
// An override that doesn't mention a key contributes the empty string, which stands
// for "no constraint" and produces a runtime with that field cleared.
func variantValueSets(overrides []*aquaregistry.Override, keys []string) map[string]map[string]struct{} {
	sets := make(map[string]map[string]struct{}, len(keys))
	for _, key := range keys {
		sets[key] = map[string]struct{}{}
	}
	for _, ov := range overrides {
		mentioned := make(map[string]string, len(ov.Variants))
		for _, v := range ov.Variants {
			mentioned[v.Key] = v.Value
		}
		for _, key := range keys {
			if val, ok := mentioned[key]; ok {
				sets[key][val] = struct{}{}
			} else {
				sets[key][""] = struct{}{}
			}
		}
	}
	return sets
}

func cartesianProduct(sets map[string]map[string]struct{}) []map[string]string {
	combos := []map[string]string{{}}
	for key, values := range sets {
		next := make([]map[string]string, 0, len(combos)*len(values))
		for _, c := range combos {
			for v := range values {
				nc := make(map[string]string, len(c)+1)
				maps.Copy(nc, c)
				nc[key] = v
				next = append(next, nc)
			}
		}
		combos = next
	}
	return combos
}

const libcKey = "libc"

func applyVariants(rt *aquaruntime.Runtime, combo map[string]string) {
	if v, ok := combo[libcKey]; ok {
		rt.LibC = v
	}
}

// variantsOf returns the variants to record for rt, or nil when there are none.
func variantsOf(rt *aquaruntime.Runtime) map[string]string {
	if rt.LibC == "" {
		return nil
	}
	return map[string]string{libcKey: rt.LibC}
}
