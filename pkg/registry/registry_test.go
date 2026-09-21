package registry_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/registry"
)

func TestAPIPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "branch",
			ref:  "main",
			want: "repos/aquaproj/aqua-registry/contents/registry.yaml?ref=main",
		},
		{
			// aqua-registry's tags contain no character that needs escaping, but a ref
			// reaches this from a flag, so it is escaped rather than trusted.
			name: "ref needing escape",
			ref:  "feat/a b",
			want: "repos/aquaproj/aqua-registry/contents/registry.yaml?ref=feat%2Fa+b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, registry.APIPath(tt.ref)); diff != "" {
				t.Errorf("APIPath is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	got, err := registry.Parse(strings.NewReader(`packages:
  - type: github_release
    repo_owner: cli
    repo_name: cli
  - name: ipinfo/cli/grepip
    type: github_release
    repo_owner: ipinfo
    repo_name: cli
  - type: cargo
    crate: sccache
`))
	if err != nil {
		t.Fatal(err)
	}
	want := &registry.Registry{
		Packages: []*registry.Package{
			{Type: "github_release", RepoOwner: "cli", RepoName: "cli"},
			{Name: "ipinfo/cli/grepip", Type: "github_release", RepoOwner: "ipinfo", RepoName: "cli"},
			{Type: "cargo"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse is wrong (-want +got):\n%s", diff)
	}
}

func TestPackage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pkg     *registry.Package
		want    string
		hasRepo bool
	}{
		{
			// aqua-registry omits `name` when it is "<repo_owner>/<repo_name>".
			name:    "name derived from the repository",
			pkg:     &registry.Package{RepoOwner: "cli", RepoName: "cli"},
			want:    "cli/cli",
			hasRepo: true,
		},
		{
			name:    "explicit name",
			pkg:     &registry.Package{Name: "ipinfo/cli/grepip", RepoOwner: "ipinfo", RepoName: "cli"},
			want:    "ipinfo/cli/grepip",
			hasRepo: true,
		},
		{
			// A cargo or http package has no GitHub repository, so it has no star count.
			name:    "no repository",
			pkg:     &registry.Package{Name: "crates.io/wasmi_cli"},
			want:    "crates.io/wasmi_cli",
			hasRepo: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, tt.pkg.PackageName()); diff != "" {
				t.Errorf("PackageName is wrong (-want +got):\n%s", diff)
			}
			if got := tt.pkg.HasRepo(); got != tt.hasRepo {
				t.Errorf("HasRepo is %v, want %v", got, tt.hasRepo)
			}
		})
	}
}
