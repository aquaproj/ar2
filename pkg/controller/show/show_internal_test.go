package show

import (
	"context"
	"errors"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// fakeRegistry stands in for aqua-registry-g2's catalogue.
type fakeRegistry struct {
	index *aquag2.Index
	ref   string
}

func (f *fakeRegistry) Index(_ context.Context, ref string) (*aquag2.Index, error) {
	f.ref = ref
	return f.index, nil
}

func catalogue() *aquag2.Index {
	return &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{
			Name: "cli/cli", ID: "1790000000",
			Description: "GitHub's official command line tool",
			Aliases:     []string{"github/hub"},
		},
		{Name: "suzuki-shunsuke/tfcmt"},
	}}
}

// Each of the things that can be in hand answers: the name, the identifier, and a name
// the package used to have.
func TestController_Show(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"cli/cli", "1790000000", "github/hub"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			reg := &fakeRegistry{index: catalogue()}
			out := &strings.Builder{}
			if err := New(reg, "main").Show(t.Context(), out, query); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range []string{
				"package:     cli/cli",
				"id:          1790000000 (2026-09-21T14:13:20Z)",
				"branch:      pkg_cli_2fcli",
				"aliases:     github/hub",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the output doesn't say %q:\n%s", want, got)
				}
			}
			if reg.ref != "main" {
				t.Errorf("read the catalogue from %q", reg.ref)
			}
		})
	}
}

// A package taken over before the registry minted identifiers says so rather than showing
// an empty field. The next reconciliation gives it one.
func TestController_Show_noIdentifierYet(t *testing.T) {
	t.Parallel()
	out := &strings.Builder{}
	err := New(&fakeRegistry{index: catalogue()}, "main").Show(t.Context(), out, "suzuki-shunsuke/tfcmt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "id:          none yet") {
		t.Errorf("the output is:\n%s", out.String())
	}
}

// A name that is nothing in the catalogue is said rather than answered with an empty entry.
func TestController_Show_notFound(t *testing.T) {
	t.Parallel()
	err := New(&fakeRegistry{index: catalogue()}, "main").Show(t.Context(), &strings.Builder{}, "nothing/here")
	if !errors.Is(err, errNotFound) {
		t.Fatalf("the error is %v", err)
	}
}
