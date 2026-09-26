package g2

import (
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

// The definition under the new name records the repository it is now, and keeps the old
// name as an alias so that a configuration still asking for it resolves.
func TestRenamedConfig(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		RepoOwner: "sst",
		RepoName:  "opencode",
	}}
	got, err := renamedConfig(cfg, "sst/opencode", "anomalyco/opencode")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"repo_owner: anomalyco\n",
		"repo_name: opencode\n",
		"- name: sst/opencode\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the definition is missing %q:\n%s", want, got)
		}
	}
}

// A package renamed twice has to answer to both of its old names, so the old name is
// kept alongside the ones it already had rather than replacing them.
func TestRenamedConfig_keepsTheOlderNames(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		Name:      "b/b",
		RepoOwner: "b",
		RepoName:  "b",
		Aliases:   []*aquaregistry.Alias{{Name: "a/a"}},
	}}
	if _, err := renamedConfig(cfg, "b/b", "c/c"); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(cfg.Aliases))
	for _, alias := range cfg.Aliases {
		names = append(names, alias.Name)
	}
	if diff := cmp.Diff([]string{"a/a", "b/b"}, names); diff != "" {
		t.Errorf("the aliases are wrong (-want +got):\n%s", diff)
	}
	// A name the definition carries is the name it is known by, so it moves too.
	if cfg.Name != "c/c" {
		t.Errorf("the name is %q, want the new one", cfg.Name)
	}
}

// Renaming to the same name twice adds the alias once, so a rename run again is a
// rename that has already happened rather than a definition saying it twice.
func TestRenamedConfig_twice(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		RepoOwner: "a",
		RepoName:  "a",
		Aliases:   []*aquaregistry.Alias{{Name: "a/a"}},
	}}
	if _, err := renamedConfig(cfg, "a/a", "b/b"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Aliases) != 1 {
		t.Errorf("got %d aliases, want the one it had", len(cfg.Aliases))
	}
}

// A package name is usually a repository and sometimes a command inside one. The first
// two parts are the repository either way.
func TestSplitRepo(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		name        string
		owner, repo string
		wantErr     bool
	}{
		{name: "cli/cli", owner: "cli", repo: "cli"},
		{name: "kubernetes/kubernetes/kubectl", owner: "kubernetes", repo: "kubernetes"},
		{name: "cli", wantErr: true},
		{name: "/cli", wantErr: true},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			owner, repo, err := splitRepo(d.name)
			if d.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if owner != d.owner || repo != d.repo {
				t.Errorf("got %s/%s, want %s/%s", owner, repo, d.owner, d.repo)
			}
		})
	}
}
