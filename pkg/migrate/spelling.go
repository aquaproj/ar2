package migrate

import (
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/asset"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// NeedsSpelling reports whether a definition names a platform in a way aqua gr can't
// read.
//
// aqua gr works out what a release supports by reading its asset names, and it knows
// the spellings it knows. luau-lang/luau calls its Linux build luau-ubuntu.zip, and
// "ubuntu" is not one of them, so it inferred a package with no Linux at all.
//
// A definition like that has to keep saying which environments the package is built
// for, because nothing else will. Every other definition can drop it: the asset
// names say it, which is why it was dropped in the first place.
func NeedsSpelling(pkgInfo *aquaregistry.PackageInfo) bool {
	if unreadable(pkgInfo.Replacements) {
		return true
	}
	for _, vo := range pkgInfo.VersionOverrides {
		if unreadable(vo.Replacements) {
			return true
		}
	}
	for _, ov := range pkgInfo.Overrides {
		if unreadable(ov.Replacements) {
			return true
		}
	}
	return false
}

// unreadable reports whether any spelling isn't one aqua gr knows.
//
// The question is put to aqua gr's own parser rather than to a list kept here, so
// that a spelling it learns stops being an exception without anyone noticing it had
// been one.
func unreadable(replacements aquaregistry.Replacements) bool {
	for key, value := range replacements {
		switch key {
		case "darwin", "linux", "windows", "amd64", "arm64":
		default:
			// Nothing resolves for the others, so how they are spelled doesn't
			// decide anything.
			continue
		}
		if value == "" || reads(key, value) {
			continue
		}
		return true
	}
	return false
}

// reads reports whether the parser takes value for the platform the definition says
// it stands for.
func reads(key, value string) bool {
	name := "tool-" + value + ".tar.gz"
	low := strings.ToLower(name)
	info := &asset.AssetInfo{}
	asset.SetOS(name, low, info)
	asset.SetArch(name, low, info)
	if info.OS == key {
		return true
	}
	return info.Arch == key
}
