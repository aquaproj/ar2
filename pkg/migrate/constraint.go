// Package migrate converts aqua-registry's package definitions into the form
// aqua-registry-g2 keeps them in.
//
// The two registries say the same things about a package; they order them
// differently. v1 lists version_overrides oldest first and ends with a catch-all,
// because entries were appended as upstream changed. g2 lists them newest first, so
// that adding a rule is an insertion at the top and leaves every existing entry
// untouched, which is what makes the file something a tool can maintain.
package migrate

import (
	"fmt"
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// ReverseVersionOverrides returns the overrides in g2's order.
//
// Reversing the list is not enough on its own. v1's entries carry upper bounds and
// rely on being read top down, so the smallest bound that matches wins; read from the
// other end, the largest bound swallows every version below it. Each entry therefore
// takes the bound of the entry below it as its own lower bound, which is the boundary
// v1 expressed by position.
//
// The catch-all at the end of a v1 list becomes the first entry and gains a lower
// bound in the same way, so it is evaluated like any other. g2 also falls back to the
// first entry when nothing matches, which is what answers a version that isn't a
// semver at all.
//
// Entries whose constraint isn't a semver bound are returned in place with their
// constraint untouched, and named in the second return value: what they meant by
// their position can't be derived, so a person has to look.
func ReverseVersionOverrides(overrides []*aquaregistry.VersionOverride) ([]*aquaregistry.VersionOverride, []string) {
	if len(overrides) == 0 {
		return nil, nil
	}
	reversed := make([]*aquaregistry.VersionOverride, 0, len(overrides))
	unconverted := []string{}
	for i, override := range slices.Backward(overrides) {
		vo := *override
		// The oldest entry keeps its constraint: nothing sits below it, so its
		// upper bound is the whole boundary.
		if i > 0 {
			lower, ok := lowerBound(overrides[i-1].VersionConstraints)
			if !ok {
				unconverted = append(unconverted, overrides[i-1].VersionConstraints)
			} else {
				vo.VersionConstraints = fmt.Sprintf("semver(%q)", lower)
			}
		}
		reversed = append(reversed, &vo)
	}
	return reversed, unconverted
}

// lowerBound turns the bound an entry has from above into the bound the entry above
// it needs from below.
//
// The two have to meet exactly: whatever the one below doesn't cover, the one above
// must. So "<= 3.0.7" becomes "> 3.0.7" and "< 4.14.0" becomes ">= 4.14.0", because
// the version on the boundary belongs to whichever of them didn't exclude it.
// Getting this wrong loses a single version to the wrong definition, which is the
// kind of thing that only shows up on the day someone installs it.
//
// Only the shape v1 writes is understood: a single semver call with one "<=" or "<"
// comparison. Anything else, such as Version == or a compound expression, is left to
// a person, because turning it into a boundary would be a guess about what the entry
// was for.
func lowerBound(constraint string) (string, bool) {
	s := strings.TrimSpace(constraint)
	inner, ok := strings.CutPrefix(s, "semver(")
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, ")")
	if !ok {
		return "", false
	}
	inner = strings.TrimSpace(inner)
	if len(inner) < 2 || inner[0] != '"' || inner[len(inner)-1] != '"' {
		return "", false
	}
	inner = inner[1 : len(inner)-1]
	if strings.Contains(inner, ",") {
		// A range already has a lower bound, so the entry wasn't relying on its
		// position and rewriting it would change what it means.
		return "", false
	}
	for _, op := range []struct {
		upper string
		lower string
	}{
		{upper: "<=", lower: "> "},
		{upper: "<", lower: ">= "},
	} {
		if v, ok := strings.CutPrefix(inner, op.upper); ok {
			v = strings.TrimSpace(v)
			if v == "" {
				return "", false
			}
			return op.lower + v, true
		}
	}
	return "", false
}
