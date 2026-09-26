package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/google/go-cmp/cmp"
)

// A definition says how a release writes a platform, and most of what it says the parser
// works out for itself. What it keeps is the part that can't be worked out.
func TestConfig_replacements(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "luau-lang",
		RepoName:  "luau",
		Replacements: aquaregistry.Replacements{
			"amd64":   "x86_64",
			"arm64":   "aarch64",
			"darwin":  "apple-darwin",
			"windows": "pc-windows-msvc",
			"linux":   "ubuntu",
		},
	}, nil)

	want := aquaregistry.Replacements{"linux": "ubuntu"}
	if diff := cmp.Diff(want, cfg.Replacements); diff != "" {
		t.Errorf("the replacements are wrong (-want +got):\n%s", diff)
	}
}

// A definition saying nothing the parser doesn't know says nothing at all, and the field
// goes rather than being written empty.
func TestConfig_replacementsAllKnown(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:         "github_release",
		RepoOwner:    "astral-sh",
		RepoName:     "uv",
		Replacements: aquaregistry.Replacements{"amd64": "x86_64", "darwin": "apple-darwin"},
	}, nil)

	if cfg.Replacements != nil {
		t.Errorf("the replacements are %+v, want none", cfg.Replacements)
	}
}
