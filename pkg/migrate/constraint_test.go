package migrate_test

import (
	"slices"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/google/go-cmp/cmp"
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

// A package whose constraints can't all be read keeps the order it has.
//
// g2 reads its entries the way v1 does — in order, first match wins — so the order
// v1 already has is the one that answers the same. containers/conmon excludes
// versions from a range with an or, and both lifting that out of the chain and
// giving it a bound answered for versions written about elsewhere.
func TestReverseVersionOverrides_keepsOrderWhenUnreadable(t *testing.T) {
	t.Parallel()
	in := []string{
		`semver("<= 2.0.19")`,
		`semver("<= 2.0.32") && semver(">= 2.0.30")`,
		`"true"`,
	}
	got, unconverted := migrate.ReverseVersionOverrides(overrides(in...))
	if diff := cmp.Diff(in, constraints(got)); diff != "" {
		t.Errorf("the entries were reordered (-want +got):\n%s", diff)
	}
	if len(unconverted) == 0 {
		t.Error("the constraint that couldn't be read wasn't reported")
	}
}

// An upper bound or'ed with the versions it also answers for is split in two: the bound
// joins the chain, and the versions are lifted out as an entry naming them is. Both keep
// what the entry says. containers/conmon writes semver("<= 2.0.23") || Version == "v2.0.25".
func TestReverseVersionOverrides_boundOrNames(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`semver("<= 2.0.19")`,
		`semver("<= 2.0.23") || Version == "v2.0.25"`,
		`"true"`,
	))
	want := []string{`Version == "v2.0.25"`, `semver("> 2.0.23")`, `semver("> 2.0.19")`, `semver("<= 2.0.19")`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
	// The lifted versions are answered by the entry they were written in.
	if got[0].Asset != `semver("<= 2.0.23") || Version == "v2.0.25"` {
		t.Errorf("the lifted entry says %q", got[0].Asset)
	}
}

// A version named by an entry that one above it already answers for in v1 is that one's,
// and lifting the names would take it, so the entry stays where it is.
func TestReverseVersionOverrides_namesAnsweredAbove(t *testing.T) {
	t.Parallel()
	in := []string{
		`semver("<= 3.0.0")`,
		`semver("<= 4.0.0") || Version == "v2.0.0"`,
		`"true"`,
	}
	got, unconverted := migrate.ReverseVersionOverrides(overrides(in...))
	if diff := cmp.Diff(in, constraints(got)); diff != "" {
		t.Errorf("the entries were reordered (-want +got):\n%s", diff)
	}
	if len(unconverted) == 0 {
		t.Error("the constraint that couldn't be split wasn't reported")
	}
}

// A list of versions names versions, as Version == does: k3d-io/k3d's
// Version in ["v3.0.0-syntax.0", "v4.1.0-alpha.2"].
func TestReverseVersionOverrides_versionIn(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`Version in ["v3.0.0-syntax.0", "v4.1.0-alpha.2"]`,
		`semver("<= 4.0.0")`,
		`"true"`,
	))
	want := []string{`Version in ["v3.0.0-syntax.0", "v4.1.0-alpha.2"]`, `semver("> 4.0.0")`, `semver("<= 4.0.0")`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
}

// Floors written newest first are g2's order already, so they are kept as they are:
// wasmerio/wasmer's semver(">= 7.2.0-alpha.2"), semver(">= 3.0.0-beta") and so on.
func TestReverseVersionOverrides_descendingFloors(t *testing.T) {
	t.Parallel()
	in := []string{
		`semver(">= 7.2.0-alpha.2")`,
		`semver(">= 3.0.0-beta")`,
		`Version == "v2.5.0"`,
		`semver(">= 2.2.1")`,
		`"true"`,
	}
	got, unconverted := migrate.ReverseVersionOverrides(overrides(in...))
	if diff := cmp.Diff(in, constraints(got)); diff != "" {
		t.Errorf("the entries were reordered (-want +got):\n%s", diff)
	}
	if len(unconverted) != 0 {
		t.Errorf("got %v as unconverted, want none", unconverted)
	}
	// Floors out of order are not a list someone wrote newest first.
	_, unconverted = migrate.ReverseVersionOverrides(overrides(`semver(">= 2.2.1")`, `semver(">= 3.0.0")`, `"true"`))
	if len(unconverted) == 0 {
		t.Error("floors out of order were taken for newest first")
	}
}
