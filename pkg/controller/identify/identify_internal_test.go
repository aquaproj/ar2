package identify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// fakeRegistry answers with the plan of each package and records what was identified.
type fakeRegistry struct {
	plans      map[string]*g2.Identification
	fail       map[string]error
	identified []string
}

func (f *fakeRegistry) PlanIdentity(_ context.Context, pkgName, id string) (*g2.Identification, error) {
	if err := f.fail[pkgName]; err != nil {
		return nil, err
	}
	plan, ok := f.plans[pkgName]
	if !ok {
		// The package is already on its id branch, which is what no plan means.
		return nil, nil //nolint:nilnil
	}
	plan.Branch = g2.IDBranchName(id)
	return plan, nil
}

func (f *fakeRegistry) Identify(_ context.Context, pkgName string, _ *g2.Identification) error {
	f.identified = append(f.identified, pkgName)
	return nil
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// definition is what a branch holds. A branch named after a package doesn't have to name it
// in the definition, which is what makes carrying it over the thing that writes it down.
func definition(t *testing.T, pkgName string, named bool) string {
	t.Helper()
	pkg := &aquaregistry.PackageInfo{RepoOwner: "owner", RepoName: "repo"}
	if named {
		pkg.Name = pkgName
	}
	b, err := yaml.Marshal(&aquag2.Config{PackageInfo: pkg})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The branches still named after a package are what there is to do, and the table says which
// packages are already on a branch named after an id.
func TestController_Identify(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{plans: map[string]*g2.Identification{
		"cli/cli": {Parent: "abc", Config: "name: cli/cli\n"},
	}}
	defs := map[string]string{
		"pkg_cli_2fcli": definition(t, "cli/cli", false),
		// Already carried over: the branch named after its id holds it, and the branch
		// named after the package is what is left behind.
		"pkg_junegunn_2ffzf": definition(t, "junegunn/fzf", false),
		"pkg_1790772768":     definition(t, "junegunn/fzf", true),
	}
	out := &strings.Builder{}

	if err := New(reg, g2.NewIdentities(discard(), defs), defs).Identify(t.Context(), discard(), out, false); err != nil {
		t.Fatal(err)
	}
	if len(reg.identified) != 1 || reg.identified[0] != "cli/cli" {
		t.Errorf("identified %v, want only cli/cli", reg.identified)
	}
	if !strings.Contains(out.String(), "1 created, 1 already there, 0 failed") {
		t.Errorf("the report is wrong:\n%s", out)
	}
}

// A dry run is what the migration is looked at through, so it must create nothing.
func TestController_Identify_dryRun(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{plans: map[string]*g2.Identification{
		"cli/cli": {Parent: "abc", Moved: []*g2.MovedFile{{
			From: "versions/kustomize/v5.8.1/registry-1.json",
			To:   "versions/kustomize_2fv5.8.1/registry-1.json",
		}}},
	}}
	defs := map[string]string{"pkg_cli_2fcli": definition(t, "cli/cli", false)}
	out := &strings.Builder{}

	if err := New(reg, g2.NewIdentities(discard(), defs), defs).Identify(t.Context(), discard(), out, true); err != nil {
		t.Fatal(err)
	}
	if len(reg.identified) != 0 {
		t.Errorf("a dry run created %v", reg.identified)
	}
	for _, want := range []string{"-> versions/kustomize_2fv5.8.1/", "1 to create"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report doesn't say %q:\n%s", want, out)
		}
	}
}

// One package that can't be carried over doesn't stop the rest: what is created is created,
// and a second run picks up where this one got to.
func TestController_Identify_reportsEveryFailure(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		plans: map[string]*g2.Identification{"cli/cli": {Parent: "abc"}},
		fail: map[string]error{
			"a/a": errors.New("no branch"),
			"b/b": errors.New("no definition"),
		},
	}
	defs := map[string]string{
		"pkg_a_2fa":     definition(t, "a/a", false),
		"pkg_cli_2fcli": definition(t, "cli/cli", false),
		"pkg_b_2fb":     definition(t, "b/b", false),
	}
	out := &strings.Builder{}

	err := New(reg, g2.NewIdentities(discard(), defs), defs).Identify(t.Context(), discard(), out, false)
	if err == nil {
		t.Fatal("the failures must be reported")
	}
	for _, want := range []string{"a/a", "b/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error doesn't name %s: %v", want, err)
		}
	}
	if len(reg.identified) != 1 || reg.identified[0] != "cli/cli" {
		t.Errorf("identified %v, want the one that could be", reg.identified)
	}
	if !strings.Contains(out.String(), "2 failed") {
		t.Errorf("the report doesn't count the failures:\n%s", out)
	}
}
