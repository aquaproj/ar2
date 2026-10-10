package generate

import (
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// merge applies the parts of aqua-registry's definition that can't be inferred from
// a release onto the PackageInfo aqua gr inferred.
//
// aqua gr reads the asset naming rule off the release itself, which is what keeps an
// upstream renaming from breaking the registry. But a release says nothing about
// which libc a build targets, how it is signed, or what the executables inside the
// archive are called, so those come from the human-written registry.yaml.
//
// base may be nil, in which case inferred is returned unchanged.
func merge(inferred, base *aquaregistry.PackageInfo) *aquaregistry.PackageInfo {
	if base == nil {
		return inferred
	}
	p := inferred.Copy()

	// Which forge the release is on is the definition's. The inference reads an asset
	// list the same way whatever answered with it, so what it says is a GitHub release
	// even when an instance's client answered.
	//
	// The host is the one the definition means rather than the one it writes: a
	// gitlab_release package says none when it is on gitlab.com, and what reads the
	// generated entry reads nothing but the entry.
	if OnInstance(base) {
		p.Type = base.Type
		p.Host = base.GetHost()
	}

	// files is not merged here. registry.yaml can set it in an override, so it has
	// to be resolved per environment rather than copied once: cli/cli's Windows
	// override puts the executable at bin/gh.exe while every other platform has it
	// under a versioned directory. resolveOne takes it from the resolved base.

	// Overrides carrying variants describe builds that differ by something the
	// release doesn't express, such as musl vs glibc. They go first because the
	// first matching override wins, and a variant override must beat the generic
	// one for the platform.
	if variants := variantOverrides(base.Overrides); len(variants) > 0 {
		p.Overrides = append(variants, p.Overrides...)
	}

	// Signing configuration. None of it can be read off the release.
	if base.Cosign != nil {
		p.Cosign = base.Cosign
	}
	if base.GitHubArtifactAttestations != nil {
		p.GitHubArtifactAttestations = base.GitHubArtifactAttestations
	}
	if base.Minisign != nil {
		p.Minisign = base.Minisign
	}
	// SLSA provenance is the one aqua gr does infer, from an asset ending in
	// .intoto.jsonl. What it can't read is whether slsa-verifier will accept the
	// provenance in it: containerd's is built by its own release workflow, which is not
	// a builder slsa-verifier trusts, and aqua has nowhere to name one. A definition
	// saying slsa_provenance is disabled is saying that, and it is what a reader of the
	// release found out rather than something the asset list shows.
	if base.SLSAProvenance != nil {
		p.SLSAProvenance = base.SLSAProvenance
	}
	return p
}

// variantOverrides returns the overrides that carry variants.
// Overrides without variants are left to the inference, which derives them from the
// release's own asset names.
func variantOverrides(overrides []*aquaregistry.Override) []*aquaregistry.Override {
	out := make([]*aquaregistry.Override, 0, len(overrides))
	for _, ov := range overrides {
		if len(ov.Variants) == 0 {
			continue
		}
		out = append(out, ov)
	}
	return out
}

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
		if ov.Cosign != nil || ov.GitHubArtifactAttestations != nil || ov.Minisign != nil {
			return ov
		}
	}
	return nil
}
