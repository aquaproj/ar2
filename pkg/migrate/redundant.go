package migrate

import (
	"log/slog"
	"reflect"
	"strconv"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
)

// dropRedundant drops the entries that answer the same as the entry their versions
// would match without them.
//
// The conversion takes out of an entry what a release can be read for -- the asset
// name, the format -- and in aqua-registry that was often all that told two entries
// apart. loeffel-io/ls-lint's ">= 2.0.0" and ">= 1.10.0-beta.1" named different assets
// there and came out empty here, and stackrox/kube-linter's entry for v0.6.8 came out
// carrying the cosign of the range v0.6.8 falls in. Each is a constraint a reader has to
// work out the consequences of before finding there are none.
//
// An entry is dropped only when every version it answers for is known to take an entry
// with the same content once it is gone:
//
//   - a lower bound followed by a lower bound or the catch-all that every version above
//     it matches as well, or
//   - an entry naming versions, when the first later entry each of them matches has the
//     same content.
//
// Anything else is left as it is, which is a longer file and never a different answer.
func dropRedundant(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, overrides []*aquaregistry.VersionOverride) []*aquaregistry.VersionOverride {
	out := overrides
	for i := 0; i < len(out)-1; {
		if !redundant(logger, pkgInfo, out, i) {
			i++
			continue
		}
		// The capacity is capped so that appending writes a new array rather than over
		// the slice the caller passed.
		out = append(out[:i:i], out[i+1:]...)
	}
	return out
}

func redundant(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, overrides []*aquaregistry.VersionOverride, i int) bool {
	vo := overrides[i]
	if vo.VersionFilter != nil {
		return false
	}
	// A version matching none of the entries takes the first, so the first stays unless
	// every version matches something.
	if i == 0 && !isCatchAll(overrides[len(overrides)-1].VersionConstraints) {
		return false
	}
	if bound, ok := upwardBound(vo.VersionConstraints); ok {
		return boundRedundant(logger, pkgInfo, vo, overrides[i+1], bound)
	}
	return namedRedundant(logger, pkgInfo, overrides, i)
}

// boundRedundant reports whether the entry after a lower bound takes every version the
// bound does, and answers the same for them.
func boundRedundant(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, vo, next *aquaregistry.VersionOverride, bound string) bool {
	if !sameContent(vo, next) {
		return false
	}
	if isCatchAll(next.VersionConstraints) {
		return true
	}
	if !upwardClosed(next.VersionConstraints) {
		return false
	}
	// Both are upward closed, so the next one takes every version above the bound when
	// it takes the bound itself. The bound has no prerelease, so neither do the versions
	// it takes, and a prerelease on the next one's bound -- ls-lint's 1.10.0-beta.1 --
	// changes nothing about those. sameContent has compared the prefixes.
	return matches(logger, pkgInfo, next, prefixOf(pkgInfo, vo)+bound)
}

// namedRedundant reports whether every version an entry names falls, without it, to an
// entry that answers the same.
func namedRedundant(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, overrides []*aquaregistry.VersionOverride, i int) bool {
	versions, ok := namedVersions(overrides[i].VersionConstraints)
	if !ok {
		return false
	}
	for _, v := range versions {
		j := firstMatch(logger, pkgInfo, overrides[i+1:], v)
		if j < 0 || !sameContent(overrides[i], overrides[i+1+j]) {
			return false
		}
	}
	return true
}

// upwardBound returns the version a constraint of the form semver(">= X") or
// semver("> X") is bounded at.
//
// A bound carrying a prerelease is left alone: whether a prerelease of another version
// satisfies it is a question about the semver library rather than about the definition,
// and getting it wrong moves a version to another entry.
func upwardBound(constraint string) (string, bool) {
	inner, ok := semverArg(constraint)
	if !ok || strings.Contains(inner, ",") {
		return "", false
	}
	for _, op := range []string{">=", ">"} {
		if v, ok := strings.CutPrefix(inner, op); ok {
			v = strings.TrimSpace(v)
			if v == "" || strings.Contains(v, "-") {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

// upwardClosed reports whether a constraint is of the form semver(">= X") or
// semver("> X"), prerelease or not.
func upwardClosed(constraint string) bool {
	inner, ok := semverArg(constraint)
	if !ok || strings.Contains(inner, ",") {
		return false
	}
	return strings.HasPrefix(inner, ">")
}

// namedVersions returns the versions a constraint of the form Version == "v" or
// Version in ["a", "b"] names.
func namedVersions(constraint string) ([]string, bool) {
	s := strings.TrimSpace(constraint)
	if rest, ok := strings.CutPrefix(s, "Version =="); ok {
		v, err := strconv.Unquote(strings.TrimSpace(rest))
		if err != nil {
			return nil, false
		}
		return []string{v}, true
	}
	rest, ok := strings.CutPrefix(s, "Version in")
	if !ok {
		return nil, false
	}
	rest = strings.TrimSpace(rest)
	inner, ok := strings.CutPrefix(rest, "[")
	if !ok {
		return nil, false
	}
	inner, ok = strings.CutSuffix(inner, "]")
	if !ok {
		return nil, false
	}
	var out []string
	for elem := range strings.SplitSeq(inner, ",") {
		v, err := strconv.Unquote(strings.TrimSpace(elem))
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, len(out) > 0
}

// firstMatch returns the index of the first entry the version matches, the way aqua
// reads them, or -1.
func firstMatch(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, overrides []*aquaregistry.VersionOverride, v string) int {
	for j, vo := range overrides {
		if matches(logger, pkgInfo, vo, v) {
			return j
		}
	}
	return -1
}

// matches evaluates an entry's constraint for a version as aqua does, prefix and all.
// A constraint that fails to evaluate matches nothing there too.
func matches(logger *slog.Logger, pkgInfo *aquaregistry.PackageInfo, vo *aquaregistry.VersionOverride, v string) bool {
	sv := v
	if prefix := prefixOf(pkgInfo, vo); prefix != "" {
		if !strings.HasPrefix(v, prefix) {
			return false
		}
		sv = strings.TrimPrefix(v, prefix)
	}
	ok, err := expr.EvaluateVersionConstraints(logger, vo.VersionConstraints, v, sv)
	return err == nil && ok
}

// sameContent reports whether two entries say the same thing about the versions they
// answer for.
func sameContent(a, b *aquaregistry.VersionOverride) bool {
	x := *a
	y := *b
	x.VersionConstraints = ""
	y.VersionConstraints = ""
	return reflect.DeepEqual(x, y)
}

// prefixOf is the prefix a version has to carry for the entry to see it.
func prefixOf(pkgInfo *aquaregistry.PackageInfo, vo *aquaregistry.VersionOverride) string {
	if vo.VersionPrefix != nil {
		return *vo.VersionPrefix
	}
	return pkgInfo.VersionPrefix
}
