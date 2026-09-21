package generate

import (
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
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

	// files: aqua gr guesses the executable name from the package name, which is
	// wrong whenever they differ (cli/cli ships "gh"), and it never knows the path
	// inside the archive. registry.yaml has both, as templates that resolve per
	// environment.
	if len(base.Files) > 0 {
		p.Files = base.Files
	}

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
