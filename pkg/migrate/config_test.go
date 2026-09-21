package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/migrate"
)

func TestConfig(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "cli",
		RepoName:  "cli",
		// v1 writes "false" here to stop the top level being a candidate. g2 says
		// the same thing by having nothing there at all.
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver("<= 2.0.0")`, Asset: "old"},
			{VersionConstraints: `"true"`, Asset: "new"},
		},
	}

	cfg, unconverted := migrate.Config(base, &genrgst.RawConfig{
		AllAssetsFilter: `not (Asset matches "^lib")`,
		VersionFilter:   `not (Version matches "^gui")`,
	})
	if len(unconverted) != 0 {
		t.Fatalf("got %v as unconverted, want none", unconverted)
	}

	if cfg.VersionConstraints != "" {
		t.Errorf("the top level constraint is %q, want none", cfg.VersionConstraints)
	}
	got := []string{cfg.VersionOverrides[0].Asset, cfg.VersionOverrides[1].Asset}
	if diff := cmp.Diff([]string{"new", "old"}, got); diff != "" {
		t.Errorf("the overrides are in the wrong order (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(`semver("> 2.0.0")`, cfg.VersionOverrides[0].VersionConstraints); diff != "" {
		t.Errorf("the newest constraint is wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(`not (Asset matches "^lib")`, cfg.AllAssetsFilter); diff != "" {
		t.Errorf("the asset filter is wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(`not (Version matches "^gui")`, cfg.VersionFilter); diff != "" {
		t.Errorf("the version filter is wrong (-want +got):\n%s", diff)
	}
	// The base is left alone: it is read again for the next package version.
	if base.VersionConstraints != "false" {
		t.Errorf("the source was modified: %q", base.VersionConstraints)
	}
}

// A package whose whole definition is at the top level still needs an override,
// because that is where g2 looks. An empty one inherits the base, which is what the
// top level meant on its own.
func TestConfig_noVersionOverride(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "cli",
		RepoName:  "cli",
		Asset:     "gh.tar.gz",
	}, nil)
	if len(cfg.VersionOverrides) != 1 {
		t.Fatalf("got %d overrides, want 1", len(cfg.VersionOverrides))
	}
	if diff := cmp.Diff(`"true"`, cfg.VersionOverrides[0].VersionConstraints); diff != "" {
		t.Errorf("the constraint is wrong (-want +got):\n%s", diff)
	}
	if cfg.VersionOverrides[0].Asset != "" {
		t.Errorf("the override carries %q, want nothing", cfg.VersionOverrides[0].Asset)
	}
}

// The registry's own filters are what aqua has been resolving with, so the scaffold
// only fills what the registry leaves empty.
func TestConfig_registryFilterWins(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:          "github_release",
		VersionFilter: `not (Version matches "^nightly")`,
		VersionPrefix: "cli-",
	}, &genrgst.RawConfig{
		VersionFilter: `not (Version matches "^gui")`,
		VersionPrefix: "gr-",
	})
	if diff := cmp.Diff(`not (Version matches "^nightly")`, cfg.VersionFilter); diff != "" {
		t.Errorf("the version filter is wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("cli-", cfg.VersionPrefix); diff != "" {
		t.Errorf("the version prefix is wrong (-want +got):\n%s", diff)
	}
}
