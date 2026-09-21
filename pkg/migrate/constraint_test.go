package migrate_test

import (
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

// A constraint that isn't a semver bound is left alone and reported. What it meant by
// sitting where it did can't be derived, so a person has to look.
func TestReverseVersionOverrides_notABound(t *testing.T) {
	t.Parallel()
	got, unconverted := migrate.ReverseVersionOverrides(overrides(
		`Version == "v2.7.0"`,
		`semver("<= 3.0.0")`,
		`"true"`,
	))
	// The entry above the one that can't be read keeps its own constraint, so it
	// now matches v2.7.0 as well. That is why it is reported.
	want := []string{`semver("> 3.0.0")`, `semver("<= 3.0.0")`, `Version == "v2.7.0"`}
	if diff := cmp.Diff(want, constraints(got)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{`Version == "v2.7.0"`}, unconverted); diff != "" {
		t.Errorf("the unconverted constraints are wrong (-want +got):\n%s", diff)
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
