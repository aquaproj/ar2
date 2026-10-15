package run

import (
	"errors"
	"log/slog"
	"maps"
	"slices"
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
		if slices.Contains(pkg.EmptyReleases, v) || unserved(logger, def, v) {
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
