package g2_test

import (
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
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

// The branch name is the id and the definition on the branch says the package, so the table
// is read from the branches rather than from anything that could disagree with them.
func TestNewIdentities(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		"pkg_1790772767": definition(t, "cli/cli"),
		"pkg_1790772903": definition(t, "kubernetes-sigs/kustomize"),
		// A branch still named after the package, which nothing writes to any more.
		"pkg_ogham_2fexa": definition(t, "ogham/exa"),
		// The claim a branch is created with, which is how a package whose definition
		// hasn't arrived is still addressable.
		"pkg_1790772768": g2.ClaimDefinition("junegunn/fzf"),
	})

	for _, tt := range []struct {
		pkgName string
		want    string
	}{
		{"cli/cli", "pkg_1790772767"},
		{"kubernetes-sigs/kustomize", "pkg_1790772903"},
		{"junegunn/fzf", "pkg_1790772768"},
	} {
		if got, ok := ids.Branch(tt.pkgName); !ok || got != tt.want {
			t.Errorf("%s is on %q (%v), want %q", tt.pkgName, got, ok, tt.want)
		}
	}
	if branch, ok := ids.Branch("ogham/exa"); ok {
		t.Errorf("a branch named after the package was read as one: %q", branch)
	}
	if pkgName, ok := ids.Package("1790772767"); !ok || pkgName != "cli/cli" {
		t.Errorf("1790772767 holds %q (%v), want cli/cli", pkgName, ok)
	}
	if head, ok := ids.HeadBranch("cli/cli"); !ok || head != "ar2_1790772767" {
		t.Errorf("the pull request comes from %q (%v)", head, ok)
	}
}

// An id is minted by stepping past the ones the branches have, so a package taken over in
// the same second as another doesn't get a branch it already holds.
func TestIdentities_Mint(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), map[string]string{
		"pkg_1790772767": definition(t, "cli/cli"),
	})
	first := ids.Mint("junegunn/fzf")
	second := ids.Mint("ogham/exa")
	if first == second {
		t.Fatalf("two packages were given the same id: %s", first)
	}
	if branch, ok := ids.Branch("junegunn/fzf"); !ok || branch != g2.IDBranchName(first) {
		t.Errorf("the minted id isn't where the package is: %q (%v)", branch, ok)
	}
}

// A package nothing holds is not on a branch, which is what makes a reader say the registry
// holds nothing for it rather than reading another package's.
func TestIdentities_unknown(t *testing.T) {
	t.Parallel()
	ids := g2.NewIdentities(slog.New(slog.DiscardHandler), nil)
	if branch, ok := ids.Branch("cli/cli"); ok {
		t.Errorf("an unheld package answered with %q", branch)
	}
	if _, ok := ids.HeadBranch("cli/cli"); ok {
		t.Error("an unheld package answered with a head branch")
	}
}

func TestBranchID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		branch string
		want   string
		ok     bool
	}{
		{branch: "pkg_1790772767", want: "1790772767", ok: true},
		// An encoded package name always holds an underscore, because a package name
		// always holds a slash.
		{branch: "pkg_cli_2fcli"},
		{branch: "pkg_"},
		{branch: "ar2_1790772767"},
		{branch: "main"},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			t.Parallel()
			got, ok := g2.BranchID(tt.branch)
			if ok != tt.ok {
				t.Fatalf("got ok=%v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The claim is what a branch is created holding, and it is not something to generate from.
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
