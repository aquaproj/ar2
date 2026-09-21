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
			// The files identify the entries: the asset name is read from the
			// release, so it doesn't survive the conversion.
			{VersionConstraints: `semver("<= 2.0.0")`, Files: []*aquaregistry.File{{Name: "old"}}},
			{VersionConstraints: `"true"`, Files: []*aquaregistry.File{{Name: "new"}}},
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
	got := []string{cfg.VersionOverrides[0].Files[0].Name, cfg.VersionOverrides[1].Files[0].Name}
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
	if cfg.VersionOverrides[0].Files != nil {
		t.Errorf("the override carries %v, want nothing", cfg.VersionOverrides[0].Files)
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

// The format is kept only where the asset name and the file disagree, which happens
// both ways round.
func TestConfig_format(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		asset  string
		format string
		want   string
	}{
		{
			name:   "the extension says it",
			asset:  "tool_{{.OS}}_{{.Arch}}.tar.gz",
			format: "tar.gz",
			want:   "",
		},
		{
			name:   "the name is built from the format",
			asset:  "tool_{{.OS}}_{{.Arch}}.{{.Format}}",
			format: "zip",
			want:   "",
		},
		{
			// An archive whose name doesn't say so would be read as raw and never
			// unpacked.
			name:   "no extension on an archive",
			asset:  "tool-{{trimV .Version}}",
			format: "tar.gz",
			want:   "tar.gz",
		},
		{
			// An extension that is part of the name would be read as an archive
			// and unpacked into nothing.
			name:   "an extension that isn't a format",
			asset:  "tool_{{.OS}}.zip",
			format: "raw",
			want:   "raw",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
				Type:      "github_release",
				RepoOwner: "foo",
				RepoName:  "foo",
				VersionOverrides: []*aquaregistry.VersionOverride{
					{VersionConstraints: `"true"`, Asset: tt.asset, Format: tt.format},
				},
			}, nil)
			if diff := cmp.Diff(tt.want, cfg.VersionOverrides[0].Format); diff != "" {
				t.Errorf("the format is wrong (-want +got):\n%s", diff)
			}
		})
	}
}
