package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
)

// sqldef/sqldef publishes mysqldef, psqldef and sqlite3def in one release, and the
// asset name was all that told the packages apart.
func TestConfig_assetsFilterFromTemplate(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("sqldef/sqldef/sqlite3def", &aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "sqldef",
		RepoName:  "sqldef",
		Asset:     "sqlite3def_{{.OS}}_{{.Arch}}.{{.Format}}",
		Format:    "tar.gz",
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Format: "zip"},
		},
	}, nil)
	if got, want := cfg.AllAssetsFilter, `Asset matches "^sqlite3def_.+_.+[.].+$"`; got != want {
		t.Errorf("all_assets_filter is %q, want %q", got, want)
	}
}

// Every template the definition gives is one the package's assets may be named by.
func TestConfig_assetsFilterFromTemplates(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("corneliusweig/rakkess/access-matrix", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "corneliusweig",
		RepoName:           "rakkess",
		VersionConstraints: "true",
		Asset:              "access-matrix-{{.Arch}}-{{.OS}}.tar.gz",
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Asset: "access-matrix-amd64-windows.zip"},
		},
	}, nil)
	want := `Asset matches "^access-matrix-.+-.+[.]tar[.]gz$" or Asset matches "^access-matrix-amd64-windows[.]zip$"`
	if got := cfg.AllAssetsFilter; got != want {
		t.Errorf("all_assets_filter is %q, want %q", got, want)
	}
}

// A package named after its repository is the one command the repository publishes,
// which the inference finds without being told.
func TestConfig_noAssetsFilterForTheRepository(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("cli/cli", &aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "cli",
		RepoName:  "cli",
		Asset:     "gh_{{trimV .Version}}_{{.OS}}_{{.Arch}}.{{.Format}}",
	}, nil)
	if cfg.AllAssetsFilter != "" {
		t.Errorf("all_assets_filter is %q, want none", cfg.AllAssetsFilter)
	}
}
