package migrate_test

import (
	"slices"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/migrate"
)

func overrides(constraints ...string) []*aquaregistry.VersionOverride {
	out := make([]*aquaregistry.VersionOverride, len(constraints))
	for i, c := range constraints {
		out[i] = &aquaregistry.VersionOverride{VersionConstraints: c, Asset: c}
	}
	return out
}

func constraints(overrides []*aquaregistry.VersionOverride) []string {
	out := make([]string, len(overrides))
	for i, vo := range overrides {
		out[i] = vo.VersionConstraints
	}
	return out
}

// tailwindlabs/tailwindcss is the shape this is for: a chain of upper bounds that
// only means what it means because of the order it is read in.
func TestReverseVersionOverrides(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`semver("<= 3.0.2")`,
		`semver("<= 3.0.7")`,
		`semver("<= 3.2.4")`,
		`semver("<= 3.2.7")`,
		`semver("<= 3.4.17")`,
		`"true"`,
	))
	want := []string{
		`semver("> 3.4.17")`,
		`semver("> 3.2.7")`,
		`semver("> 3.2.4")`,
		`semver("> 3.0.7")`,
		`semver("> 3.0.2")`,
		`semver("<= 3.0.2")`,
	}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
	// The entries keep what they say about the package; only the boundary moves.
	if diff := cmp.Diff(`"true"`, got[0].Asset); diff != "" {
		t.Errorf("the first entry is wrong (-want +got):\n%s", diff)
	}
}

// nodejs/node is the two-entry shape, where reversing alone would already be right.
func TestReverseVersionOverrides_pair(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`semver("<= 24.10.0")`,
		`"true"`,
	))
	want := []string{`semver("> 24.10.0")`, `semver("<= 24.10.0")`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
}

// An entry that names a version goes first, where matching one version means
// answering for that version and nothing else.
//
// Reversed into the chain it sank below the ranges, which then answered for the very
// version it was written for. It also left the entry above it without a bound, so
// that one kept the bound it had from above and swallowed everything below.
func TestReverseVersionOverrides_namesAVersion(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`Version == "v2.7.0"`,
		`semver("<= 3.0.0")`,
		`"true"`,
	))
	want := []string{`Version == "v2.7.0"`, `semver("> 3.0.0")`, `semver("<= 3.0.0")`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	// Nothing is left for a person: naming a version says what it answers for
	// wherever it sits.
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
}

// A range already carries its own lower bound, so it never relied on its position and
// rewriting it would change what it matches.
func TestReverseVersionOverrides_range(t *testing.T) {
	t.Parallel()
	_, unconverted := migrate.ReverseVersionOverrides(overrides(
		`semver("> 1.0.0, <= 2.0.0")`,
		`"true"`,
	))
	if diff := cmp.Diff([]string{`semver("> 1.0.0, <= 2.0.0")`}, unconverted); diff != "" {
		t.Errorf("the unconverted constraints are wrong (-want +got):\n%s", diff)
	}
}

// The bounds have to meet exactly. A "<" entry excludes the version on its boundary,
// so the entry above it has to include it; suzuki-shunsuke/tfcmt is the case in
// aqua-registry, where v4.14.0 belongs to the entry above "< 4.14.0".
func TestReverseVersionOverrides_exclusiveBound(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`semver("< 4.14.0")`,
		`semver("<= 4.14.12")`,
		`"true"`,
	))
	want := []string{`semver("> 4.14.12")`, `semver(">= 4.14.0")`, `semver("< 4.14.0")`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
}

// A constraint that is a semver call and something else is not a bound.
//
// openziti/zrok bounds a range and excludes a prefix in the same expression. Cutting
// that at the first and last bracket produced a bound with the rest of the
// expression trailing off it — a constraint that evaluates to nothing and matches no
// version, which is how the package lost the attestations its newest releases carry.
func TestReverseVersionOverrides_compoundExpression(t *testing.T) {
	t.Parallel()
	compound := `semver("< 2.0.0") and not (Version startsWith "v2.0.0-rc")`
	got, _ := migrate.ReverseVersionOverrides(overrides(
		compound,
		`"true"`,
	))
	for _, c := range constraints(got) {
		if strings.Contains(c, `\"`) {
			t.Errorf("a constraint was cut into something that doesn't parse: %s", c)
		}
	}
	// It keeps its own constraint, which is what it always meant.
	if !slices.Contains(constraints(got), compound) {
		t.Errorf("the expression was rewritten: %v", constraints(got))
	}
}
