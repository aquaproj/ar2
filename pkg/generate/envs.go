package generate

import (
	"log/slog"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/aqua/v2/pkg/template"
)

// environments are the ones aqua resolves a package for. A release that names a
// platform in a way aqua gr can't read is missing from what it inferred, so each of
// these is offered the definition's spelling and kept if the release turns out to
// have it.
const (
	archAmd64 = "amd64"
	archArm64 = "arm64"
)

var environments = []struct{ os, arch string }{ //nolint:gochecknoglobals
	{"linux", archAmd64},
	{"linux", archArm64},
	{"darwin", archAmd64},
	{"darwin", archArm64},
	{"windows", archAmd64},
	{"windows", archArm64},
}

// recoverEnvs puts back the environments aqua gr couldn't read the names of.
//
// aqua gr works out what a release supports by reading its asset names, and it knows
// the spellings it knows. luau-lang/luau calls its Linux build luau-ubuntu.zip, which
// it can't read, so it inferred a package with no Linux at all — not a wrong asset
// name for Linux, no Linux.
//
// The definition says what the spelling means: linux is written ubuntu. That is used
// here as a name the parser didn't have, by rendering the inferred template with it
// and seeing whether the release has a file of that name. Nothing is assumed: an
// environment is put back only when the asset it would download is there.
//
// Both halves are needed and neither is enough. A definition saying Linux is
// supported doesn't say what the file is called, and the inferred template would
// have rendered luau-linux.zip; a file that happens to exist doesn't say the
// platform is one the package is built for.
func recoverEnvs(logger *slog.Logger, inferred, base *aquaregistry.PackageInfo, assetNames map[string]struct{}, version string) {
	if base == nil || inferred == nil || inferred.Asset == "" || len(base.Replacements) == 0 {
		return
	}
	for _, env := range environments {
		if supports(inferred, env.os, env.arch) {
			continue
		}
		// The definition says which environments exist; the release says what the
		// file is called. Without the first, a template with no {{.Arch}} in it
		// renders the same name for every architecture and every one of them looks
		// as though it were there: luau publishes no windows/arm64 build and its
		// windows asset would have answered for one.
		if !supports(base, env.os, env.arch) {
			continue
		}
		name, ok := renderFor(inferred, base, env.os, env.arch, version)
		if !ok {
			continue
		}
		if _, ok := assetNames[name]; !ok {
			continue
		}
		logger.Info("the release names an environment in a way aqua gr can't read",
			"os", env.os, "arch", env.arch, "asset", name)
		inferred.SupportedEnvs = append(inferred.SupportedEnvs, env.os+"/"+env.arch)
		adopt(inferred, base, env.os, env.arch)
	}
}

// renderFor renders the inferred asset name for one environment, spelling it the way
// the definition says.
func renderFor(inferred, base *aquaregistry.PackageInfo, os, arch, version string) (string, bool) {
	art := &template.Artifact{
		Version: version,
		SemVer:  strings.TrimPrefix(version, "v"),
		OS:      spell(inferred, base, os),
		Arch:    spell(inferred, base, arch),
		Format:  inferred.GetFormat(),
	}
	name, err := template.Render(inferred.Asset, art, &runtime.Runtime{GOOS: os, GOARCH: arch})
	if err != nil {
		return "", false
	}
	return name, name != ""
}

// spell returns how a name is written, preferring what was inferred: those
// replacements came from the asset names themselves and are what the template was
// built around.
func spell(inferred, base *aquaregistry.PackageInfo, name string) string {
	if v, ok := inferred.Replacements[name]; ok {
		return v
	}
	if v, ok := base.Replacements[name]; ok {
		return v
	}
	return name
}

// adopt takes the spellings the definition has for an environment that was put back.
func adopt(inferred, base *aquaregistry.PackageInfo, names ...string) {
	for _, name := range names {
		v, ok := base.Replacements[name]
		if !ok {
			continue
		}
		if _, ok := inferred.Replacements[name]; ok {
			continue
		}
		if inferred.Replacements == nil {
			inferred.Replacements = aquaregistry.Replacements{}
		}
		inferred.Replacements[name] = v
	}
}

// supports reports whether the inferred definition already covers an environment.
func supports(inferred *aquaregistry.PackageInfo, os, arch string) bool {
	if len(inferred.SupportedEnvs) == 0 {
		return true
	}
	for _, env := range inferred.SupportedEnvs {
		switch env {
		case "all", os, os + "/" + arch, arch:
			return true
		}
	}
	return false
}
