package g2

import (
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

func TestEscapedVersionPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{
			// The version was written as it is, so its slash became two directories.
			name: "a version with a slash in it",
			path: "versions/kustomize/v5.8.1/registry-1.json",
			want: "versions/kustomize_2fv5.8.1/registry-1.json",
			ok:   true,
		},
		{
			// The underscore is escaped too, so a tag holding one moves as well.
			name: "a version with an underscore in it",
			path: "versions/apps_v1.80.0/registry-1.json",
			want: "versions/apps_5fv1.80.0/registry-1.json",
			ok:   true,
		},
		{
			name: "a version that needs no escape",
			path: "versions/v1.0.0/registry-1.json",
		},
		{
			name: "a version already escaped",
			path: "versions/kustomize_2fv5.8.1/registry-1.json",
		},
		{
			name: "a file sitting directly under versions",
			path: "versions/README.md",
		},
		{
			name: "a file that isn't a version's",
			path: "registry.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := escapedVersionPath(tt.path)
			if ok != tt.ok {
				t.Fatalf("got ok=%v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A branch named after an id doesn't say which package it holds, so the definition does.
func TestNamedConfig(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		RepoOwner: "cli", RepoName: "cli",
	}}
	got, err := namedConfig(cfg, "cli/cli")
	if err != nil {
		t.Fatal(err)
	}
	if want := "name: cli/cli\n"; !strings.Contains(got, want) {
		t.Errorf("the definition doesn't name the package:\n%s", got)
	}
}

// A definition already naming its package is left as it is, so the branch starts at the
// commit the package is already on.
func TestNamedConfig_alreadyNamed(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{Name: "cli/cli"}}
	got, err := namedConfig(cfg, "cli/cli")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("", got); diff != "" {
		t.Errorf("there was nothing to write (-want +got):\n%s", diff)
	}
}

func TestNamedConfig_noConfig(t *testing.T) {
	t.Parallel()
	if _, err := namedConfig(nil, "cli/cli"); err == nil {
		t.Fatal("a branch with no definition has nothing to name")
	}
}
