package g2_test

import (
	"context"
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
)

func definition(t *testing.T, pkgName string) string {
	t.Helper()
	b, err := yaml.Marshal(&aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		Name:      pkgName,
		RepoOwner: "owner",
		RepoName:  "repo",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The directory is named after the id and the definition in it says the package, so the
// table is read from the packages rather than from anything that could disagree with them.
func TestNewIdentities(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		"1790772767": definition(t, "cli/cli"),
		"1790772903": definition(t, "kubernetes-sigs/kustomize"),
		// A claim, which a package copied from a branch that never got its definition
		// may still hold. It names the package all the same.
		"1790772768": g2.ClaimDefinition("junegunn/fzf"),
	}, map[string]string{
		// A package whose definition is still in an open pull request.
		"1790772999": definition(t, "ogham/exa"),
		// A pull request naming a package the default branch already holds under
		// another id. The default branch wins.
		"1790773000": definition(t, "cli/cli"),
		// A pull request whose definition couldn't be read. Its id is taken all the
		// same.
		"1790773001": "",
	})

	for _, tt := range []struct {
		pkgName string
		want    string
	}{
		{"cli/cli", "pkgs/67/1790772767"},
		{"kubernetes-sigs/kustomize", "pkgs/03/1790772903"},
		{"junegunn/fzf", "pkgs/68/1790772768"},
		{"ogham/exa", "pkgs/99/1790772999"},
	} {
		if got, ok := ids.Dir(tt.pkgName); !ok || got != tt.want {
			t.Errorf("%s is in %q (%v), want %q", tt.pkgName, got, ok, tt.want)
		}
	}
	if pkgName, ok := ids.Package("1790772767"); !ok || pkgName != "cli/cli" {
		t.Errorf("1790772767 holds %q (%v), want cli/cli", pkgName, ok)
	}
	if head, ok := ids.HeadBranch("cli/cli"); !ok || head != "ar2_1790772767" {
		t.Errorf("the pull request comes from %q (%v)", head, ok)
	}
}

// An id is minted by stepping past the ones taken, so a package taken over in the same second
// as another doesn't get an id it already has -- including one only a pull request names.
func TestIdentities_Mint(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		"1790772767": definition(t, "cli/cli"),
	}, map[string]string{"1790772768": ""})
	first := ids.Mint("junegunn/fzf")
	second := ids.Mint("ogham/exa")
	if first == second {
		t.Fatalf("two packages were given the same id: %s", first)
	}
	for _, id := range []string{first, second} {
		if id == "1790772767" || id == "1790772768" {
			t.Errorf("a taken id was minted again: %s", id)
		}
	}
	if dir, ok := ids.Dir("junegunn/fzf"); !ok || dir != g2.PackageDir(first) {
		t.Errorf("the minted id isn't where the package is: %q (%v)", dir, ok)
	}
}

// A package nothing holds has no directory, which is what makes a reader say the registry
// holds nothing for it rather than reading another package's.
func TestIdentities_unknown(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), nil, nil)
	if dir, ok := ids.Dir("cli/cli"); ok {
		t.Errorf("an unheld package answered with %q", dir)
	}
	if _, ok := ids.HeadBranch("cli/cli"); ok {
		t.Error("an unheld package answered with a head branch")
	}
}

// fakeDefinitions serves a default branch and open pull requests from memory.
type fakeDefinitions struct {
	dirs  []string
	heads []string
	blobs map[string]string
}

func (f *fakeDefinitions) Subtrees(_ context.Context, expression string) ([]string, error) {
	if expression != "main:pkgs" {
		return nil, nil
	}
	return f.dirs, nil
}

func (f *fakeDefinitions) Blobs(_ context.Context, _ *slog.Logger, expressions []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range expressions {
		if text, ok := f.blobs[e]; ok {
			out[e] = text
		}
	}
	return out, nil
}

func (f *fakeDefinitions) OpenPullRequestHeads(context.Context) ([]string, error) {
	return f.heads, nil
}

// The table is what the default branch holds and what open pull requests are bringing. A
// package taken over by a pull request that hasn't merged keeps its id, so the next run
// doesn't mint it another.
func TestReadIdentities(t *testing.T) {
	t.Parallel()
	defs := &fakeDefinitions{
		dirs: []string{
			"67/1790772767",
			// Not where its id says it is, so nothing would find it.
			"00/1790772768",
		},
		heads: []string{
			"ar2_1790772767",
			"ar2_1790772999_v1.0.0",
			"ar2_index",
			"ar2_remove_1790772767",
			"renovate/foo",
		},
		blobs: map[string]string{
			"main:pkgs/67/1790772767/registry.yaml":                  definition(t, "cli/cli"),
			"main:pkgs/00/1790772768/registry.yaml":                  definition(t, "junegunn/fzf"),
			"ar2_1790772999_v1.0.0:pkgs/99/1790772999/registry.yaml": definition(t, "ogham/exa"),
		},
	}
	ids, held, err := g2.ReadIdentities(context.Background(), slog.New(slog.DiscardHandler), defs)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]string{"1790772767": definition(t, "cli/cli")}, held); diff != "" {
		t.Errorf("the definitions on the default branch (-want +got):\n%s", diff)
	}
	if id, ok := ids.ID("ogham/exa"); !ok || id != "1790772999" {
		t.Errorf("the package in a pull request is %q (%v)", id, ok)
	}
	if _, ok := ids.ID("junegunn/fzf"); ok {
		t.Error("a misplaced directory was read as a package")
	}
}

func TestPackageDir(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"1790772769": "pkgs/69/1790772769",
		"1790773000": "pkgs/00/1790773000",
		"7":          "pkgs/07/7",
	} {
		if got := g2.PackageDir(id); got != want {
			t.Errorf("PackageDir(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestHeadBranchID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		branch string
		want   string
		ok     bool
	}{
		{branch: "ar2_1790772767", want: "1790772767", ok: true},
		{branch: "ar2_1790772767_v1.0.0", want: "1790772767", ok: true},
		{branch: "ar2_index"},
		{branch: "ar2_remove_1790772767"},
		{branch: "pkg_1790772767"},
		{branch: "main"},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			t.Parallel()
			got, ok := g2.HeadBranchID(tt.branch)
			if ok != tt.ok {
				t.Fatalf("got ok=%v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A claim names a package and is not something to generate from.
func TestIsClaim(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(g2.ClaimDefinition("cli/cli")), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "cli/cli" {
		t.Errorf("the claim names %q", cfg.Name)
	}
	if !g2.IsClaim(cfg) {
		t.Error("the claim wasn't read as one")
	}
	whole := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(definition(t, "cli/cli")), whole); err != nil {
		t.Fatal(err)
	}
	if g2.IsClaim(whole) {
		t.Error("a definition was read as a claim")
	}
}
