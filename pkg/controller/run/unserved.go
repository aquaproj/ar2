package run

import (
	"errors"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/state"
)

// emptyReleaseSettles is how long after a release is published one with no assets is
// taken to stay that way. A release is made before its assets are uploaded, and a run that
// reads it in between finds it empty; a day later the upload has finished or isn't coming.
const emptyReleaseSettles = 24 * time.Hour

// settled is the versions there is nothing to do for: the ones the registry holds, and
// the ones it never will. A version the definition says has no asset, or says to use
// another package for, is one of the latter, and so is one whose release turned out to
// hold no asset at all.
//
// They are passed over without costing the turn anything, and the package counts as
// caught up once only they are left. Trying them instead spent a turn of every run on
// each: cert-manager/cert-manager/cmctl's newest releases are the ones its definition
// sends to cert-manager/cmctl, and it never got past them.
func settled(logger *slog.Logger, def *definition, pkg *state.Package, versions []string, existing map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(existing))
	maps.Copy(out, existing)
	for _, v := range versions {
		if _, ok := out[v]; ok {
			continue
		}
		if slices.Contains(pkg.EmptyReleases, v) || unserved(logger, def, v) || tiedUnder(pkg, def, v) {
			out[v] = struct{}{}
		}
	}
	return out
}

// unserved says whether the definition says the version has nothing to install: no_asset,
// or an error_message saying what to use instead. A definition that can't be resolved for
// the version says nothing, and generating it is what finds out.
func unserved(logger *slog.Logger, def *definition, version string) bool {
	if def == nil || def.config == nil {
		return false
	}
	pkgInfo, err := def.config.SetVersion(logger, version)
	if err != nil {
		return false
	}
	return pkgInfo.NoAsset || pkgInfo.ErrorMessage != ""
}

// emptyRelease says whether the error is a release that holds no asset and has had a day
// to get one, and records it so that no later run tries it again.
func emptyRelease(pkg *state.Package, version string, err error, now time.Time) bool {
	e, ok := errors.AsType[*generate.EmptyReleaseError](err)
	if !ok || e.PublishedAt.IsZero() || now.Sub(e.PublishedAt) < emptyReleaseSettles {
		return false
	}
	if !slices.Contains(pkg.EmptyReleases, version) {
		pkg.EmptyReleases = append(pkg.EmptyReleases, version)
	}
	return true
}

// tiedUnder says whether the version tied under the definition as it is now.
func tiedUnder(pkg *state.Package, def *definition, version string) bool {
	fp, ok := pkg.Ties[version]
	return ok && fp == tieFingerprint(def)
}

// tied records a version whose every environment tied, under the definition it tied under.
func tied(pkg *state.Package, def *definition, version string) {
	if pkg.Ties == nil {
		pkg.Ties = map[string]string{}
	}
	pkg.Ties[version] = tieFingerprint(def)
}

// tieFingerprint is what a tie was recorded under: the definition, and the aqua that read
// the asset names.
//
// A tie is settled by the definition or by how names are read, and the second is aqua's
// pkg/asset. Recorded under the definition alone, the versions that tied stayed passed
// over after aqua learned to tell their assets apart -- ko-build/ko's
// ko_0.15.4_Darwin_x86_64.tar.gz beside ko_Darwin_x86_64.tar.gz -- since nothing about the
// definition had changed.
func tieFingerprint(def *definition) string {
	return def.fingerprint() + "@" + selectorVersion()
}

// selectorVersion is the version of the aqua module ar2 was built with, or "" when the
// binary doesn't say.
var selectorVersion = sync.OnceValue(func() string { //nolint:gochecknoglobals
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path != "github.com/aquaproj/aqua/v2" {
			continue
		}
		if dep.Replace != nil {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return ""
})
