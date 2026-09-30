package identify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
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
	plan.Branch = "pkg_" + id
	return plan, nil
}

func (f *fakeRegistry) Identify(_ context.Context, pkgName string, _ *g2.Identification) error {
	f.identified = append(f.identified, pkgName)
	return nil
}

func index(t *testing.T, packages ...*aquag2.IndexPackage) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	b, err := json.Marshal(&aquag2.Index{Packages: packages})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestController_Identify(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{plans: map[string]*g2.Identification{
		"cli/cli": {Parent: "abc", Config: "name: cli/cli\n"},
	}}
	path := index(t,
		&aquag2.IndexPackage{Name: "cli/cli", ID: "1790772767"},
		// A package already on its id branch has no plan, and is not touched again.
		&aquag2.IndexPackage{Name: "junegunn/fzf", ID: "1790772768"},
	)
	out := &strings.Builder{}

	if err := New(reg).Identify(t.Context(), discard(), out, path, false); err != nil {
		t.Fatal(err)
	}
	if diff := []string{"cli/cli"}; len(reg.identified) != 1 || reg.identified[0] != diff[0] {
		t.Errorf("identified %v, want %v", reg.identified, diff)
	}
	for _, want := range []string{"cli/cli\tpkg_1790772767", "1 created, 1 already there, 0 failed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report doesn't say %q:\n%s", want, out)
		}
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
	out := &strings.Builder{}

	if err := New(reg).Identify(t.Context(), discard(), out,
		index(t, &aquag2.IndexPackage{Name: "cli/cli", ID: "1790772767"}), true); err != nil {
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

// One package that can't be identified doesn't stop the rest: what is created is created,
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
	out := &strings.Builder{}

	err := New(reg).Identify(t.Context(), discard(), out, index(t,
		&aquag2.IndexPackage{Name: "a/a", ID: "1"},
		&aquag2.IndexPackage{Name: "cli/cli", ID: "2"},
		&aquag2.IndexPackage{Name: "b/b", ID: "3"},
	), false)
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
