package generate

import (
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// overrideSigning applies the signing an override names for one environment.
//
// A release can't be read for how it is signed, so the definition says it, and it may
// say something different for one environment than for the rest: FiloSottile/age's
// v1.3.1 darwin/amd64 asset was signed by a workflow that backfilled it rather than by
// the one that built the release.
//
// Such an override can't ride along in Overrides. The first override matching an
// environment is the one applied, and it is applied whole, so a signing-only override
// put in front of the inferred override for the same platform would take the asset name
// with it. What it applies to is the resolved entry, which is per environment already.
func overrideSigning(reg *Registry, base *aquaregistry.PackageInfo) {
	if base == nil {
		return
	}
	for _, asset := range reg.Assets {
		ov := signingOverride(base.Overrides, asset)
		if ov == nil {
			continue
		}
		if ov.Cosign != nil {
			asset.Cosign = ov.Cosign
		}
		if ov.GitHubArtifactAttestations != nil {
			asset.GitHubArtifactAttestations = ov.GitHubArtifactAttestations
		}
		if ov.Minisign != nil {
			asset.Minisign = ov.Minisign
		}
		if ov.Packslip != nil {
			asset.Packslip = ov.Packslip
		}
	}
}

// signingOverride returns the first override matching the entry's environment and
// carrying signing configuration.
//
// Variants are left out of the match: which libc an entry needs is read from the
// executables, which happens after this, and an override selecting on one describes a
// build rather than a signature.
func signingOverride(overrides []*aquaregistry.Override, asset *Asset) *aquaregistry.Override {
	rt := &runtime.Runtime{GOOS: asset.OS, GOARCH: asset.Arch}
	for _, ov := range overrides {
		if len(ov.Variants) != 0 || !ov.MatchPlatform(rt) {
			continue
		}
		if ov.Cosign != nil || ov.GitHubArtifactAttestations != nil || ov.Minisign != nil || ov.Packslip != nil {
			return ov
		}
	}
	return nil
}
