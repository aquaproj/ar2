package migrate

import (
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
// all. The format and the replacements are read off the asset names, which works
// until a name carries no extension or spells an architecture in a way aqua doesn't
// know.
//
// Every other type is left alone. A release says nothing about an http URL or a Go
// module path, so for those the definition is all there is.
func trimInferred(pkgInfo *aquaregistry.PackageInfo) *aquaregistry.PackageInfo {
	if pkgInfo.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return pkgInfo
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
		Overrides:                  keepOverrides(pkgInfo.Overrides, pkgInfo.Asset),
		Cosign:                     pkgInfo.Cosign,
		SLSAProvenance:             pkgInfo.SLSAProvenance,
		Minisign:                   pkgInfo.Minisign,
		GitHubArtifactAttestations: pkgInfo.GitHubArtifactAttestations,
		Checksum:                   signedChecksum(pkgInfo.Checksum),

		Format:       correctedFormat(pkgInfo.Asset, pkgInfo.Format),
		Replacements: pkgInfo.Replacements,
		AppendExt:    pkgInfo.AppendExt,
		WindowsExt:   pkgInfo.WindowsExt,

		NoAsset: pkgInfo.NoAsset,
		Vars:    pkgInfo.Vars,
		Build:   pkgInfo.Build,
	}
	trimmed.VersionOverrides = make([]*aquaregistry.VersionOverride, 0, len(pkgInfo.VersionOverrides))
	for _, vo := range pkgInfo.VersionOverrides {
		trimmed.VersionOverrides = append(trimmed.VersionOverrides, trimOverride(vo, pkgInfo.Asset))
	}
	return trimmed
}

func trimOverride(vo *aquaregistry.VersionOverride, parentAsset string) *aquaregistry.VersionOverride {
	asset := vo.Asset
	if asset == "" {
		asset = parentAsset
	}
	return &aquaregistry.VersionOverride{
		VersionConstraints: vo.VersionConstraints,
		// The prefix and the filter are part of deciding what the constraint sees,
		// so they belong with it.
		VersionPrefix: vo.VersionPrefix,
		VersionFilter: vo.VersionFilter,

		Files:                      vo.Files,
		Overrides:                  keepOverrides(vo.Overrides, asset),
		Cosign:                     vo.Cosign,
		SLSAProvenance:             vo.SLSAProvenance,
		Minisign:                   vo.Minisign,
		GitHubArtifactAttestations: vo.GitHubArtifactAttestations,
		Checksum:                   signedChecksum(vo.Checksum),

		Format:       correctedFormat(asset, vo.Format),
		Replacements: vo.Replacements,
		AppendExt:    vo.AppendExt,
		WindowsExt:   vo.WindowsExt,

		NoAsset:      vo.NoAsset,
		ErrorMessage: vo.ErrorMessage,
		Vars:         vo.Vars,
		Build:        vo.Build,
	}
}

// signedChecksum keeps a checksum file only when its signature can be verified.
//
// An unsigned one is fetched from the same place as the asset, so believing it adds
// nothing to downloading the asset and hashing it, which is what happens anyway. The
// definition doesn't need to say where a file nothing will read lives.
func signedChecksum(chksum *aquaregistry.Checksum) *aquaregistry.Checksum {
	if chksum == nil {
		return nil
	}
	if chksum.GetCosign() == nil && chksum.GetMinisign() == nil && chksum.GetGitHubArtifactAttestations() == nil {
		return nil
	}
	return chksum
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

// keepOverrides drops the overrides that only describe an asset name, and trims what
// is left.
//
// An override narrowing by os or arch to change the asset is saying what the
// release's own asset list says. One that carries files, variants or signing is
// saying something the asset list doesn't: where the executable sits inside the
// archive, which libc a build needs, or how it is signed.
//
// The ones kept are trimmed the same way the rest of the definition is, since being
// worth keeping for its files doesn't make an override's asset name worth keeping.
func keepOverrides(overrides []*aquaregistry.Override, parentAsset string) []*aquaregistry.Override {
	out := make([]*aquaregistry.Override, 0, len(overrides))
	for _, ov := range overrides {
		if len(ov.Variants) == 0 && len(ov.Files) == 0 &&
			ov.Cosign == nil && ov.SLSAProvenance == nil && ov.Minisign == nil &&
			ov.GitHubArtifactAttestations == nil {
			continue
		}
		out = append(out, trimOverrideByRuntime(ov, parentAsset))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func trimOverrideByRuntime(ov *aquaregistry.Override, parentAsset string) *aquaregistry.Override {
	asset := ov.Asset
	if asset == "" {
		asset = parentAsset
	}
	return &aquaregistry.Override{
		GOOS:     ov.GOOS,
		GOArch:   ov.GOArch,
		Envs:     ov.Envs,
		Variants: ov.Variants,

		Files:                      ov.Files,
		Cosign:                     ov.Cosign,
		SLSAProvenance:             ov.SLSAProvenance,
		Minisign:                   ov.Minisign,
		GitHubArtifactAttestations: ov.GitHubArtifactAttestations,
		Checksum:                   signedChecksum(ov.Checksum),

		Format:       correctedFormat(asset, ov.Format),
		Replacements: ov.Replacements,
		WindowsExt:   ov.WindowsExt,
		AppendExt:    ov.AppendExt,
		Vars:         ov.Vars,
	}
}
