package migrate

import (
	"reflect"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
)

// withFallback ends the list with an entry every version matches.
//
// v1 answers a version that matches no override with the top level itself. g2 has no
// top level, and answers with its first entry instead — which, once the entries that
// name a version are lifted to the front, is one written for a particular version.
// bazelbuild/bazel-watcher's V0.26.9 matches nothing and took an entry saying
// no_asset, for a release that has assets.
//
// What the entry carries is what v1 would have answered. A list ending in a
// constraint every version matches never reaches the top level, so a tag that is no
// version at all — superradcompany/microsandbox publishes another component's tags
// in the same repository — takes that last entry; the conversion gives it a bound
// and it would otherwise stop catching them. A list ending in a bound does reach the
// top level, and an entry carrying nothing inherits it.
func withFallback(overrides, original []*aquaregistry.VersionOverride) []*aquaregistry.VersionOverride {
	if len(overrides) == 0 {
		return overrides
	}
	// A list already ending in one needs no other. Reordering gives the catch-all a
	// lower bound, which is why the end of a converted list usually isn't one; a list
	// that couldn't be reordered is returned as it stands and still ends in v1's,
	// carrying everything v1 wrote in it. Appending there wrote that entry twice.
	if isCatchAll(overrides[len(overrides)-1].VersionConstraints) {
		return overrides
	}
	fallback := &aquaregistry.VersionOverride{VersionConstraints: catchAll}
	if len(original) > 0 {
		if newest := original[len(original)-1]; isCatchAll(newest.VersionConstraints) {
			copied := *newest
			copied.VersionConstraints = catchAll
			fallback = &copied
		}
	}
	return append(overrides, fallback)
}

// topLevelFirst puts v1's top level back as the entry it was.
//
// v1 evaluates the top level before any override and uses it when it matches, so a
// top level with a real constraint is a candidate like any other and has to be one
// here too. BurntSushi/xsv bounds its top level at ">= 0.10.3" and keeps musl in an
// override for everything from 0.10.0; without this, 0.10.3 took the override and
// looked for a musl build that the newer releases don't have.
//
// The entry carries nothing but the constraint, because an override inherits the
// base, which is what the top level was.
//
// A top level constrained to "false" is v1 saying it is not a candidate, which is
// what g2 means by having no top level at all.
func topLevelFirst(constraint string, overrides []*aquaregistry.VersionOverride) []*aquaregistry.VersionOverride {
	c := strings.TrimSpace(constraint)
	if c == "" || c == "false" || c == `"false"` {
		return overrides
	}
	return append([]*aquaregistry.VersionOverride{{VersionConstraints: c}}, overrides...)
}

// catchAll is the constraint of an override that every version matches.
//
// It is the expression true, not the string "true": a constraint is evaluated as an
// expression and has to come out a boolean, so a quoted one fails to parse and the
// override it belongs to matches nothing at all. YAML quotes it on the way out,
// because an unquoted true there would be a boolean rather than the expression.
const catchAll = "true"

// Config builds a package's aqua-registry-g2 definition from its aqua-registry one.
//
// The second return value names the constraints that couldn't be turned into a
// boundary. The result is still usable, but what those entries matched in v1 isn't
// what they match here, so a person has to look at them.
func Config(base *aquaregistry.PackageInfo, scaffold *genrgst.RawConfig) (*g2.Config, []string) {
	pkgInfo := trimInferred(base.Copy())

	// g2 has no version_constraint at the top level: it is the base the overrides
	// inherit from and never a candidate itself. v1 writes "false" there to stop it
	// being one, which is the same intent said the other way round.
	pkgInfo.VersionConstraints = ""

	// v1 reads the top level's constraint first and, when it has none, stops there:
	// the overrides are never consulted. XcodesOrg/xcodes has one saying no_asset
	// for a single version, which has never applied to anything. Carried over it
	// would become the entry every version falls back to, and the package would
	// resolve to no asset at all.
	if strings.TrimSpace(base.VersionConstraints) == "" && len(pkgInfo.VersionOverrides) > 0 {
		pkgInfo.VersionOverrides = nil
		unreachable := make([]string, 0, len(base.VersionOverrides))
		for _, vo := range base.VersionOverrides {
			unreachable = append(unreachable, vo.VersionConstraints)
		}
		cfg := &g2.Config{PackageInfo: pkgInfo}
		cfg.VersionOverrides = []*aquaregistry.VersionOverride{{VersionConstraints: catchAll}}
		applyScaffold(cfg, scaffold)
		return cfg, unreachable
	}

	// The trimmed overrides, not the source ones: reversing the originals would put
	// back everything the trim just took out.
	overrides, unconverted := ReverseVersionOverrides(pkgInfo.VersionOverrides)
	if len(overrides) == 0 || allEmpty(overrides) {
		// A package whose definition is entirely at the top level still needs an
		// override, because that is where g2 looks. An empty one inherits the whole
		// base, which is what the top level meant on its own.
		//
		// A list whose entries all say nothing is the same thing said many times.
		// Arriven/db1000n's ten overrides turned into ten bare constraints once
		// what a release can be read for was taken out of them, which is a file
		// that tells a reader there are ten cases to think about and then describes
		// none of them.
		overrides = []*aquaregistry.VersionOverride{{VersionConstraints: catchAll}}
	}
	pkgInfo.VersionOverrides = withFallback(topLevelFirst(base.VersionConstraints, overrides), pkgInfo.VersionOverrides)

	cfg := &g2.Config{PackageInfo: pkgInfo}
	applyScaffold(cfg, scaffold)
	return cfg, unconverted
}

// applyScaffold carries over what aqua gr was configured with.
//
// The filters are what a release can't be read for: which of its assets is the
// command, and which of its tags are versions of it. They only fill fields the
// registry doesn't already set, because the registry's own value is what aqua has
// been resolving with and the two are meant to agree.
func applyScaffold(cfg *g2.Config, scaffold *genrgst.RawConfig) {
	if scaffold == nil {
		return
	}
	cfg.AllAssetsFilter = scaffold.AllAssetsFilter
	cfg.AssetFilters = assetFilters(scaffold.VersionOverrides)
	if cfg.VersionFilter == "" {
		cfg.VersionFilter = scaffold.VersionFilter
	}
	if cfg.VersionPrefix == "" {
		cfg.VersionPrefix = scaffold.VersionPrefix
	}
}

// assetFilters carries the scaffold's version axis into the definition.
//
// Which asset is the command can change over a package's history, and the definition on
// the branch is what later runs read: the scaffold in aqua-registry is fetched only
// while the definition is being converted for the first time.
func assetFilters(overrides []*genrgst.RawVersionOverride) []*g2.AssetFilter {
	if len(overrides) == 0 {
		return nil
	}
	out := make([]*g2.AssetFilter, 0, len(overrides))
	for _, vo := range overrides {
		if vo == nil {
			continue
		}
		out = append(out, &g2.AssetFilter{
			VersionConstraint: vo.VersionConstraint,
			AllAssetsFilter:   vo.AllAssetsFilter,
		})
	}
	return out
}

// allEmpty reports whether every override says nothing beyond which versions it is
// for.
//
// Only every one of them. An empty override among others that aren't is doing
// something: it says these versions take nothing, and dropping it would let them
// fall through to an older entry that does carry fields.
func allEmpty(overrides []*aquaregistry.VersionOverride) bool {
	for _, vo := range overrides {
		if !emptyOverride(vo) {
			return false
		}
	}
	return true
}

// emptyOverride reports whether an override carries anything but its constraint.
//
// It is compared against a zero value rather than checked field by field, so that a
// field added to the definition later counts without anyone remembering to add it
// here.
func emptyOverride(vo *aquaregistry.VersionOverride) bool {
	rest := *vo
	rest.VersionConstraints = ""
	return reflect.DeepEqual(rest, aquaregistry.VersionOverride{})
}
