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
// An entry that names versions instead of bounding them — Version == "v0.5.15", or
// semver("= 0.5.0") — is not part of that chain. It can't give the entry above it a
// bound, and reversed it sinks below the ranges, which then answer for the very
// version it was written for: Aloxaf/silicon's v0.5.0 took the entry meant for
// everything newer and looked for pc-windows-gnu instead of pc-windows-msvc.
//
// So those entries are lifted out and put first, where matching one version means
// answering for that version and nothing else. What is left is a chain of bounds with
// no gaps in it, which is what the derivation needs.
//
// The catch-all at the end of a v1 list becomes the first of the ranges and gains a
// lower bound in the same way, so it is evaluated like any other. g2 also falls back
// to the first entry when nothing matches, which is what answers a version that isn't
// a semver at all.
//
// The second return value names the constraints that were lifted out without naming
// a version — a compound range, say. Those matched more than one version, so where
// they sit changes what they answer for, and a person has to look.
func ReverseVersionOverrides(overrides []*aquaregistry.VersionOverride) ([]*aquaregistry.VersionOverride, []string) {
	if len(overrides) == 0 {
		return nil, nil
	}
	pinned, bounded, unconverted := partition(overrides)
	if len(unconverted) > 0 {
		// Something here is neither a bound nor a name, so where it sits is what it
		// meant and nothing can be derived about it. g2 reads its entries the way v1
		// does — in order, first match wins — so the order it already has is the one
		// that answers the same, and an entry every version matches goes on the end
		// in place of the top level v1 fell back to.
		//
		// Reordering these was worth trying and wasn't close. containers/conmon
		// excludes versions from a range with an or, and both lifting that out and
		// giving it a bound answered for versions written about elsewhere.
		return inOriginalOrder(overrides), unconverted
	}

	reversed := make([]*aquaregistry.VersionOverride, 0, len(overrides))
	reversed = append(reversed, pinned...)
	for i, override := range slices.Backward(bounded) {
		vo := *override
		// The oldest entry keeps its constraint: nothing sits below it, so its
		// upper bound is the whole boundary.
		if i > 0 {
			if lower, ok := lowerBound(bounded[i-1].VersionConstraints); ok {
				vo.VersionConstraints = fmt.Sprintf("semver(%q)", lower)
			}
		}
		reversed = append(reversed, &vo)
	}
	return reversed, unconverted
}

// inOriginalOrder returns the entries as they are, which is what v1 meant by them.
func inOriginalOrder(overrides []*aquaregistry.VersionOverride) []*aquaregistry.VersionOverride {
	out := make([]*aquaregistry.VersionOverride, 0, len(overrides))
	for _, vo := range overrides {
		copied := *vo
		out = append(out, &copied)
	}
	return out
}

// partition splits the entries into the ones that name versions and the ones that
// bound them.
//
// An entry belongs to the chain only if the entry above it can take a bound from it.
// Anything else has to come out, or the entry above keeps the bound it had from
// above and swallows everything below it.
func partition(overrides []*aquaregistry.VersionOverride) (pinned, bounded []*aquaregistry.VersionOverride, unconverted []string) {
	unconverted = []string{}
	// The newest entry is v1's catch-all and becomes g2's first. Nothing above it
	// takes a bound from it, so it stays in the chain whatever its constraint says.
	last := len(overrides) - 1
	for i, vo := range overrides {
		if i == last {
			continue
		}
		if _, ok := lowerBound(vo.VersionConstraints); ok {
			bounded = append(bounded, vo)
			continue
		}
		pinned = append(pinned, vo)
		if !namesAVersion(vo.VersionConstraints) {
			unconverted = append(unconverted, vo.VersionConstraints)
		}
	}
	bounded = append(bounded, overrides[last])
	return pinned, bounded, unconverted
}

// namesAVersion reports whether a constraint matches one version rather than a range
// of them.
//
// Those are the ones that can be lifted to the top without changing what anything
// else answers for: whatever else matches that version, this was written for it.
func namesAVersion(constraint string) bool {
	s := strings.TrimSpace(constraint)
	if strings.HasPrefix(s, "Version ==") || strings.HasPrefix(s, "Version==") {
		return true
	}
	inner, ok := strings.CutPrefix(s, "semver(")
	if !ok {
		return false
	}
	inner = strings.TrimSpace(strings.TrimSuffix(inner, ")"))
	inner = strings.Trim(inner, `"`)
	return strings.HasPrefix(strings.TrimSpace(inner), "=") &&
		!strings.HasPrefix(strings.TrimSpace(inner), "==")
}

// semverArg returns what a constraint that is one semver call asks for.
//
// The whole constraint has to be that call and nothing else. openziti/zrok bounds a
// range and excludes a prefix in the same expression, and cutting that at the first
// and last bracket produced a bound with the rest of the expression trailing off it
// — a constraint that evaluates to nothing and so matches no version, which is how
// the package lost the attestations its newest releases carry.
func semverArg(constraint string) (string, bool) {
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
	if strings.Contains(inner, `"`) {
		return "", false
	}
	return inner, true
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
	inner, ok := semverArg(constraint)
	if !ok {
		return "", false
	}
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
