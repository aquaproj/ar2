package generate

import (
	"maps"
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// provenanceSuffix is how a SLSA provenance published beside a release is named.
const provenanceSuffix = ".intoto.jsonl"

// inferSLSA records the SLSA provenance the release publishes for each entry.
//
// A provenance is named after what it attests: foo_linux_amd64.tar.gz.intoto.jsonl is the
// asset's own, and a provenance named after nothing the release holds, such as
// multiple.intoto.jsonl, is the whole release's. An entry gets its asset's own provenance,
// or the release's when it has none.
//
// A definition saying anything about SLSA provenance wins, including one saying it is
// disabled: whether slsa-verifier accepts the provenance can't be read off the asset list.
// containerd's is built by its own release workflow, which is not a builder slsa-verifier
// trusts.
func inferSLSA(reg *Registry, base *aquaregistry.PackageInfo, names map[string]struct{}) {
	if base != nil && base.SLSAProvenance != nil {
		return
	}
	release := releaseProvenance(names)
	for _, asset := range reg.Assets {
		// A provenance's type says where it is downloaded from, and what is read here is
		// a GitHub release.
		if asset.SLSAProvenance != nil || !inGitHubRelease(asset) {
			continue
		}
		provenance := asset.Asset + provenanceSuffix
		if _, ok := names[provenance]; !ok {
			provenance = release
		}
		if provenance == "" {
			continue
		}
		asset.SLSAProvenance = &aquaregistry.SLSAProvenance{
			Type:  aquaregistry.PkgInfoTypeGitHubRelease,
			Asset: &provenance,
		}
	}
}

// releaseProvenance is the provenance of the whole release: the first one named after
// nothing the release holds, or "".
func releaseProvenance(names map[string]struct{}) string {
	for _, name := range slices.Sorted(maps.Keys(names)) {
		subject, ok := strings.CutSuffix(name, provenanceSuffix)
		if !ok {
			continue
		}
		if _, ok := names[subject]; !ok {
			return name
		}
	}
	return ""
}

// inGitHubRelease says whether the entry downloads an asset of a release on github.com.
func inGitHubRelease(asset *Asset) bool {
	return asset.Asset != "" && asset.Type == aquaregistry.PkgInfoTypeGitHubRelease && asset.Host == ""
}
