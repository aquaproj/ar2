package migrate

import (
	"maps"
	"strings"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// trimInferred drops what a release is read for.
//
// A github_release package's asset naming, the environments it supports and whether
// an arm64 machine falls back to an amd64 build are all worked out from the release's
// own asset list, per version. Carrying them over would be writing down an answer
// that is derived anyway, and a wrong one as soon as upstream changes: which
// environments a version supports is a property of that version, not of the package.
//
// What stays is what the asset list can't answer, and what it answers wrongly often
// enough to be worth correcting by hand. The executables inside the archive, the
// builds that differ by libc, and how an asset is signed are not in the asset list at
// all. Where the checksum file is doesn't stay either, for the other reason: a
// generated entry carries the digest of the asset, so nothing reads that file. The format and the replacements are read off the asset names, which works
// until a name carries no extension or spells an architecture in a way aqua doesn't
// know.
//
// Every other type is left alone. A release says nothing about an http URL or a Go
// module path, so for those the definition is all there is.
func trimInferred(pkgInfo *aquaregistry.PackageInfo) *aquaregistry.PackageInfo {
	if pkgInfo.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		// A release says nothing about an http URL or a Go module path, so the
		// definition is all there is -- except for the checksum file, which nothing
		// reads whatever the package is.
		return withoutChecksum(pkgInfo)
	}
	trimmed := &aquaregistry.PackageInfo{
		Name:      pkgInfo.Name,
		Type:      pkgInfo.Type,
		RepoOwner: pkgInfo.RepoOwner,
		RepoName:  pkgInfo.RepoName,
		// How a user may refer to the package, and what index.json is built from.
		Aliases:     pkgInfo.Aliases,
		SearchWords: pkgInfo.SearchWords,
		Description: pkgInfo.Description,
		Link:        pkgInfo.Link,
		// Which tags are versions of this package at all.
		VersionFilter: pkgInfo.VersionFilter,
		VersionPrefix: pkgInfo.VersionPrefix,

		Files:                      pkgInfo.Files,
		Overrides:                  keepOverrides(pkgInfo.Overrides, &parentOf{asset: pkgInfo.Asset, format: pkgInfo.Format, replacements: pkgInfo.Replacements}),
		Cosign:                     pkgInfo.Cosign,
		SLSAProvenance:             pkgInfo.SLSAProvenance,
		Minisign:                   pkgInfo.Minisign,
		Packslip:                   pkgInfo.Packslip,
		GitHubArtifactAttestations: Attestations(pkgInfo.GitHubArtifactAttestations),

		Format:       correctedFormat(pkgInfo.Asset, pkgInfo.Format),
		Replacements: unguessableSpellings(pkgInfo.Replacements),
		AppendExt:    pkgInfo.AppendExt,
		WindowsExt:   pkgInfo.WindowsExt,

		NoAsset: pkgInfo.NoAsset,
		Vars:    pkgInfo.Vars,
		Build:   pkgInfo.Build,
		// Which environments the package is for. A release can publish an asset for one
		// that doesn't hold the package, which its asset names don't say.
		SupportedEnvs: pkgInfo.SupportedEnvs,
	}
	trimmed.VersionOverrides = make([]*aquaregistry.VersionOverride, 0, len(pkgInfo.VersionOverrides))
	for _, vo := range pkgInfo.VersionOverrides {
		trimmed.VersionOverrides = append(trimmed.VersionOverrides, trimOverride(vo, pkgInfo))
	}
	return trimmed
}

func trimOverride(vo *aquaregistry.VersionOverride, pkgInfo *aquaregistry.PackageInfo) *aquaregistry.VersionOverride {
	parent := &parentOf{asset: vo.Asset, format: vo.Format, replacements: vo.Replacements}
	if parent.asset == "" {
		parent.asset = pkgInfo.Asset
	}
	if parent.format == "" {
		parent.format = pkgInfo.Format
	}
	if parent.replacements == nil {
		parent.replacements = pkgInfo.Replacements
	}
	asset := parent.asset
	return &aquaregistry.VersionOverride{
		VersionConstraints: vo.VersionConstraints,
		// The prefix and the filter are part of deciding what the constraint sees,
		// so they belong with it.
		VersionPrefix: vo.VersionPrefix,
		VersionFilter: vo.VersionFilter,

		Files:                      vo.Files,
		Overrides:                  keepOverrides(vo.Overrides, parent),
		Cosign:                     vo.Cosign,
		SLSAProvenance:             vo.SLSAProvenance,
		Minisign:                   vo.Minisign,
		Packslip:                   vo.Packslip,
		GitHubArtifactAttestations: Attestations(vo.GitHubArtifactAttestations),

		Format:       correctedFormat(asset, vo.Format),
		Replacements: unguessableSpellings(vo.Replacements),
		AppendExt:    vo.AppendExt,
		WindowsExt:   vo.WindowsExt,

		NoAsset:       vo.NoAsset,
		ErrorMessage:  vo.ErrorMessage,
		Vars:          vo.Vars,
		Build:         vo.Build,
		SupportedEnvs: vo.SupportedEnvs,
	}
}

// withoutChecksum returns the definition with nothing said about the checksum file.
//
// aqua-registry verifies an asset against that file, signature and all. Here every entry
// carries the digest of the asset itself, taken when the entry was generated and checked
// again by the registry's CI on a machine of the environment the entry is for, so
// nothing ever fetches the file -- a generated entry has nowhere to say that it exists.
// A definition saying where it is says it to nobody.
func withoutChecksum(pkgInfo *aquaregistry.PackageInfo) *aquaregistry.PackageInfo {
	out := pkgInfo.Copy()
	out.Checksum = nil
	out.VersionOverrides = make([]*aquaregistry.VersionOverride, 0, len(pkgInfo.VersionOverrides))
	for _, vo := range pkgInfo.VersionOverrides {
		copied := *vo
		copied.Checksum = nil
		out.VersionOverrides = append(out.VersionOverrides, &copied)
	}
	return out
}

// correctedFormat keeps a format only when it disagrees with what the asset name
// says.
//
// aqua reads the format off the extension, so a name ending in .tar.gz needs nothing
// said about it. What has to be written down is where the name and the file disagree,
// which happens both ways: an asset with no extension that is an archive all the
// same, read as raw and never unpacked, and an asset whose extension is part of its
// name rather than a format, read as an archive and unpacked into nothing.
//
// A name built from {{.Format}} was named from the format in the first place, and the
// inference reads the real asset names, so there is nothing to correct.
func correctedFormat(asset, format string) string {
	if format == "" || asset == "" {
		return format
	}
	if strings.Contains(asset, "{{.Format}}") {
		return ""
	}
	if _, inferred := aquaasset.RemoveExtFromAsset(asset); inferred == format {
		return ""
	}
	return format
}

// parentOf is what an override inherits from where it is written: the asset, the
// format and the replacements of the version or the package.
type parentOf struct {
	asset        string
	format       string
	replacements aquaregistry.Replacements
}

// keepOverrides drops the overrides that say nothing, and trims what is left.
//
// goos, goarch, envs and variants are the conditions an override matches on; they change
// nothing by themselves. So an override carrying only those says nothing here, however it
// is written. In aqua-registry it can still mean something -- an aqua too old to know
// variants drops the key and matches on the rest, so an empty entry placed first keeps
// such an aqua on the asset it always had -- and nothing that reads this registry is that
// old.
//
// What an override can say is where the executable sits inside the archive, how the build
// is signed, and which asset it is. That last one is usually what the release's own asset
// list says, so it is trimmed -- except where the condition is a variant, because nothing
// in an asset's name says which libc it was built against, and where the asset's name
// says no OS, because then the release can't be read for which platform it is. There the
// asset is the whole of what the override is for.
func keepOverrides(overrides []*aquaregistry.Override, parent *parentOf) []*aquaregistry.Override {
	out := make([]*aquaregistry.Override, 0, len(overrides))
	for _, ov := range overrides {
		if !saysSomething(ov) {
			continue
		}
		out = append(out, trimOverrideByRuntime(ov, parent))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// saysSomething reports whether an override changes what it matches, rather than only
// saying what it matches.
func saysSomething(ov *aquaregistry.Override) bool {
	if len(ov.Files) != 0 || ov.Cosign != nil || ov.SLSAProvenance != nil ||
		ov.Minisign != nil || ov.Packslip != nil || ov.GitHubArtifactAttestations != nil {
		return true
	}
	// A variant's asset, or an asset whose name says no OS: the release's asset list
	// can't be read for either.
	return len(ov.Variants) != 0 && (ov.Asset != "" || ov.URL != "") || unnamedOSOverride(ov)
}

func trimOverrideByRuntime(ov *aquaregistry.Override, parent *parentOf) *aquaregistry.Override {
	asset := ov.Asset
	if asset == "" {
		asset = parent.asset
	}
	out := &aquaregistry.Override{
		GOOS:     ov.GOOS,
		GOArch:   ov.GOArch,
		Envs:     ov.Envs,
		Variants: ov.Variants,

		Files:                      ov.Files,
		Cosign:                     ov.Cosign,
		SLSAProvenance:             ov.SLSAProvenance,
		Minisign:                   ov.Minisign,
		Packslip:                   ov.Packslip,
		GitHubArtifactAttestations: Attestations(ov.GitHubArtifactAttestations),

		Format:       overrideFormat(asset, ov),
		Replacements: unguessableSpellings(ov.Replacements),
		WindowsExt:   ov.WindowsExt,
		AppendExt:    ov.AppendExt,
		Vars:         ov.Vars,
	}
	if unnamedOSOverride(ov) {
		// An asset whose name says no OS: the release can't be read for which
		// platform it is, so the definition says it. See addUnnamedOSAssets.
		// Its format too, which the definition may say once for every platform and
		// the conversion takes off the top level when the names are built from it.
		out.Asset = ov.Asset
		out.Format = ov.Format
		if out.Format == "" {
			out.Format = parent.format
		}
		// And every spelling the asset is rendered with, the ones the parser knows
		// included: the asset is rendered for the platform alone, with nothing read
		// off the release to supply them. aqua lays an override's replacements over
		// the definition's, so both go in.
		out.Replacements = maps.Clone(parent.replacements)
		if out.Replacements == nil && len(ov.Replacements) != 0 {
			out.Replacements = aquaregistry.Replacements{}
		}
		maps.Copy(out.Replacements, ov.Replacements)
	}
	if len(ov.Variants) != 0 {
		// The one asset the release can't be read for: which libc a build was made
		// against is in no asset name the parser knows how to read.
		out.Asset = ov.Asset
		out.URL = ov.URL
		if ov.Asset != "" {
			// Nor its format. aqua applies the first override that matches, so the
			// format inferred for the OS is in another override that this one keeps
			// from applying: UsefulSoftwareCo/executor's musl build, named with
			// {{.Format}}, was looked for as a zip when it is a tar.gz.
			out.Format = ov.Format
		}
	}
	return out
}

// overrideFormat is the format an override keeps.
//
// An override naming the executables inside the archive describes one archive, and the
// format says which when a release publishes the build in two: tamasfe/taplo's Windows
// build is a .gz holding taplo-windows-x86_64 and a .zip holding taplo.exe. The asset
// name being built from {{.Format}} says nothing then, since both are.
func overrideFormat(asset string, ov *aquaregistry.Override) string {
	if len(ov.Files) != 0 {
		return ov.Format
	}
	return correctedFormat(asset, ov.Format)
}

// Attestations writes the signing workflow under the name aqua reads first.
//
// aqua-registry still holds entries written as signer-workflow, which aqua accepts
// and has deprecated. A registry being written now has no reason to carry the old
// spelling into its first version of a file.
//
// being translated away from.
//
//nolint:staticcheck // reading the deprecated name is the point: it is what is
func Attestations(a *aquaregistry.GitHubArtifactAttestations) *aquaregistry.GitHubArtifactAttestations {
	if a == nil || a.SignerWorkflow3 == "" {
		return a
	}
	out := *a
	out.SignerWorkflow2 = a.SignerWorkflow()
	out.SignerWorkflow3 = ""
	return &out
}

// unguessableSpellings keeps the replacements that say something the release can't be
// read for, and drops the rest.
//
// A replacement says how a release writes a platform: darwin as osx, amd64 as x86_64.
// aqua-registry's definitions say it because aqua resolves an asset name from a template
// there, and every spelling the template uses has to be declared. Here nothing is
// resolved from a template: the asset names are read off the release, and the parser works
// most of those spellings out for itself. Carrying them over would fill the definition
// with copies of something that can be looked at -- and with something that can go wrong
// later, when a spelling the definition insists on stops being the one the release uses.
//
// What can't be worked out stays. luau-lang/luau calls its Linux build luau-ubuntu.zip,
// and the replacement is the only thing that makes that asset Linux at all. A spelling
// nobody has taught the parser can't be derived from the release that uses it, so that
// line is a person's and it is kept.
//
// Dropping one that mattered would show up as a version covering fewer environments than
// the one before it, which is left for review rather than merged.
func unguessableSpellings(replacements aquaregistry.Replacements) aquaregistry.Replacements {
	out := aquaregistry.Replacements{}
	for platform, spelling := range replacements {
		if aquaasset.KnowsSpelling(platform, spelling) {
			continue
		}
		out[platform] = spelling
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// UnnamedOS says whether an asset's name says no OS, so that the inference can't tell
// which platform it is for: koalaman/shellcheck's shellcheck-{{.Version}}.{{.Format}} for
// Windows. A name built from the OS in any form ({{.OS}}, {{title .OS}}) says one.
func UnnamedOS(asset string) bool {
	if asset == "" || strings.Contains(asset, ".OS") {
		return false
	}
	return aquaasset.ParseAssetName(asset, "").OS == ""
}

// unnamedOSOverride says whether an override names, for one OS, an asset whose name says
// no OS: what generating falls back on when the inference finds nothing for the OS. An
// override for no OS, or one selecting on envs or variants, isn't read that way.
func unnamedOSOverride(ov *aquaregistry.Override) bool {
	return ov.GOOS != "" && len(ov.Envs) == 0 && len(ov.Variants) == 0 && UnnamedOS(ov.Asset)
}
