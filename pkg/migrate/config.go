package migrate

import (
	"reflect"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
)

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
	pkgInfo.VersionOverrides = overrides

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
	if cfg.VersionFilter == "" {
		cfg.VersionFilter = scaffold.VersionFilter
	}
	if cfg.VersionPrefix == "" {
		cfg.VersionPrefix = scaffold.VersionPrefix
	}
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
