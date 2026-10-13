package generate

import (
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/ar2/pkg/migrate"
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
	//
	// An override saying the format of the archive it names the executables of comes
	// next, for the same reason: see formatOverrides.
	if overrides := slices.Concat(variantOverrides(base.Overrides), formatOverrides(base.Overrides, p.Overrides)); len(overrides) > 0 {
		p.Overrides = append(overrides, p.Overrides...)
	}

	// An asset whose name says no OS is one the inference can't give to any, and the
	// definition can: see addUnnamedOSAssets.
	addUnnamedOSAssets(p, base)

	// The extension aqua gives files[].src on Windows. The release's asset names say
	// nothing about what is inside the archive: sass/dart-sass's Windows build runs from
	// dart-sass/sass.bat, and without it the entry looks for dart-sass/sass.exe.
	if base.WindowsExt != "" {
		p.WindowsExt = base.WindowsExt
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
	if base.Packslip != nil {
		p.Packslip = base.Packslip
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

// formatOverrides returns an override for each one in the definition that says which
// format holds the executables it names, taking the rest from the inferred override for
// the same platform.
//
// The inference picks one format per platform from the asset names, and a release can
// publish the same build in two: tamasfe/taplo's Windows build is both a .gz holding
// taplo-windows-x86_64 and a .zip holding taplo.exe. Whichever the inference picks, the
// definition's files describe one of them, and an override giving both the format and the
// files is saying which. Taking the files without the format looks for taplo.exe in the
// .gz.
//
// An override without files is left to the inference: a format alone says nothing the
// asset names can't, and the definition's may only be out of date.
func formatOverrides(base, inferred []*aquaregistry.Override) []*aquaregistry.Override {
	var out []*aquaregistry.Override
	for _, ov := range base {
		if ov.Format == "" || len(ov.Files) == 0 || len(ov.Variants) != 0 || len(ov.Envs) != 0 {
			continue
		}
		o := &aquaregistry.Override{}
		if matched := inferredFor(inferred, ov); matched != nil {
			copied := *matched
			o = &copied
		}
		o.GOOS = ov.GOOS
		o.GOArch = ov.GOArch
		o.Envs = nil
		o.Format = ov.Format
		out = append(out, o)
	}
	return out
}

// addUnnamedOSAssets gives a platform the asset the definition names for it, when the
// inference found none there.
//
// The inference reads which platform an asset is for off its name, and a name can say
// none: koalaman/shellcheck's Windows build is shellcheck-v0.11.0.zip, and ip7z/7zip's
// console binaries are in 7z2604-extra.7z. Such a platform is simply left out of what is
// inferred. An override in the definition naming the asset for the platform is what puts
// it back. It is used only where the inference has nothing, so a release that starts
// naming its assets is read off the release again.
func addUnnamedOSAssets(p, base *aquaregistry.PackageInfo) {
	if p.SupportedEnvs == nil {
		// The inference covers every platform.
		return
	}
	for _, ov := range base.Overrides {
		if ov.GOOS == "" || len(ov.Envs) != 0 || len(ov.Variants) != 0 || !migrate.UnnamedOS(ov.Asset) {
			continue
		}
		format := ov.Format
		if format == "" {
			format = base.Format
		}
		for _, goarch := range unnamedOSArchs(ov) {
			env := ov.GOOS + "/" + goarch
			if p.CheckSupportedEnvs(ov.GOOS, goarch, env) {
				continue
			}
			// Right after the variant overrides, ahead of what the inference and the
			// format overrides say for the OS: aqua applies the first override that
			// matches, and one for the OS alone would otherwise win.
			p.Overrides = slices.Insert(p.Overrides, leadingVariants(p.Overrides), &aquaregistry.Override{
				GOOS:   ov.GOOS,
				GOArch: goarch,
				Asset:  ov.Asset,
				Format: format,
			})
			p.SupportedEnvs = append(slices.Clone(p.SupportedEnvs), env)
		}
	}
}

// unnamedOSArchs returns the architectures an override's asset is for. One naming an
// architecture is for it; one whose name is built from the architecture is for both; one
// saying neither is the OS's one build, which is amd64 far more often than not -- an arm64
// machine that can run it is the emulation's to say.
func unnamedOSArchs(ov *aquaregistry.Override) []string {
	switch {
	case ov.GOArch != "":
		return []string{ov.GOArch}
	case strings.Contains(ov.Asset, ".Arch"):
		return []string{"amd64", "arm64"}
	default:
		return []string{"amd64"}
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

// inferredFor returns the inferred override aqua would apply on the platform the
// definition's override is for, or nil when there is none.
func inferredFor(inferred []*aquaregistry.Override, ov *aquaregistry.Override) *aquaregistry.Override {
	for _, o := range inferred {
		if len(o.Envs) != 0 || len(o.Variants) != 0 {
			continue
		}
		if (o.GOOS == "" || o.GOOS == ov.GOOS) && (o.GOArch == "" || o.GOArch == ov.GOArch) {
			return o
		}
	}
	return nil
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
