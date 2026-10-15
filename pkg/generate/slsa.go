package generate

import (
	"maps"
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// provenanceSuffix is how a SLSA provenance published beside a release is named.
const provenanceSuffix = ".intoto.jsonl"

// inferSLSA records how the provenance the release publishes for each entry is verified.
//
// A provenance is named after what it attests: foo_linux_amd64.tar.gz.intoto.jsonl is the
// asset's own, and a provenance named after nothing the release holds, such as
// multiple.intoto.jsonl, is the whole release's. An entry gets its asset's own provenance,
// or the release's when it has none.
//
// What the provenance is decides how: kind reads it. One slsa-github-generator built is an
// SLSA provenance. One a release workflow signed with its GitHub Actions identity is a
// GitHub artifact attestation, which gh verifies and slsa-verifier doesn't --
// awslabs/aidlc-workflows publishes one. Anything else nothing aqua runs can verify, so
// the entry says nothing about it.
//
// A definition saying anything about SLSA provenance wins, including one saying it is
// disabled, and so does one saying anything about attestations.
func inferSLSA(reg *Registry, base *aquaregistry.PackageInfo, names map[string]struct{}, kind func(asset string) provenanceInfo) {
	if base != nil && base.SLSAProvenance != nil {
		return
	}
	attestationsDeclared := base != nil && base.GitHubArtifactAttestations != nil
	release := releaseProvenance(names)
	for _, asset := range reg.Assets {
		// A provenance's type says where it is downloaded from, and what is read here is
		// a GitHub release.
		if asset.SLSAProvenance != nil || !inGitHubRelease(asset) {
			continue
		}
		name := provenanceFor(asset, names, release)
		if name == "" {
			continue
		}
		applyProvenance(asset, name, kind(name), attestationsDeclared)
	}
}

// provenanceFor is the provenance of an entry: its asset's own, or else the release's, or
// "" for none.
func provenanceFor(asset *Asset, names map[string]struct{}, release string) string {
	name := asset.Asset + provenanceSuffix
	if _, ok := names[name]; ok {
		return name
	}
	return release
}

// applyProvenance records on the entry how its provenance is verified.
func applyProvenance(asset *Asset, name string, p provenanceInfo, attestationsDeclared bool) {
	switch p.kind {
	case provenanceSLSA:
		asset.SLSAProvenance = &aquaregistry.SLSAProvenance{
			Type:  aquaregistry.PkgInfoTypeGitHubRelease,
			Asset: &name,
		}
	case provenanceAttestation:
		if !attestationsDeclared && asset.GitHubArtifactAttestations == nil {
			asset.GitHubArtifactAttestations = &aquaregistry.GitHubArtifactAttestations{SignerWorkflow2: p.signerWorkflow}
		}
	case provenanceNone:
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
