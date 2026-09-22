package migrate_test

import (
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
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
			{VersionConstraints: "true", Files: []*aquaregistry.File{{Name: "new"}}},
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
	if diff := cmp.Diff("true", cfg.VersionOverrides[0].VersionConstraints); diff != "" {
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
				// "false" is how a registry says the top level is not a candidate,
				// which is what makes the overrides the ones that answer.
				VersionConstraints: "false",
				VersionOverrides: []*aquaregistry.VersionOverride{
					{VersionConstraints: "true", Asset: tt.asset, Format: tt.format},
				},
			}, nil)
			if diff := cmp.Diff(tt.want, cfg.VersionOverrides[0].Format); diff != "" {
				t.Errorf("the format is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

// A list of overrides that all say nothing is the same thing said many times.
//
// Arriven/db1000n's ten overrides turned into ten bare constraints once what a
// release can be read for was taken out of them: a file telling a reader there are
// ten cases to think about and then describing none of them.
func TestConfig_allOverridesEmpty(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "Arriven",
		RepoName:           "db1000n",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `Version == "v0.5.15"`, Asset: "db1000n-{{.Version}}.tar.gz", Format: "tar.gz"},
			{VersionConstraints: `semver("<= 0.7.0")`, Asset: "db1000n_{{.Version}}.tar.gz", Format: "tar.gz"},
		},
	}, nil)

	if len(cfg.VersionOverrides) != 1 {
		t.Fatalf("got %d overrides, want the one that catches everything:\n%+v", len(cfg.VersionOverrides), cfg.VersionOverrides)
	}
	if got := cfg.VersionOverrides[0].VersionConstraints; got != "true" {
		t.Errorf("the constraint is %q, want the one that always matches", got)
	}
}

// An empty override among others that aren't is doing something: it says these
// versions take nothing, and dropping it would let them fall through to an older
// entry that does carry fields.
func TestConfig_someOverridesEmpty(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "cli",
		RepoName:           "cli",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver("< 2.0.0")`, Asset: "gh_{{.Version}}.tar.gz", Format: "tar.gz"},
			{
				VersionConstraints: `semver(">= 2.0.0")`, Asset: "gh_{{.Version}}.tar.gz", Format: "tar.gz",
				Replacements: aquaregistry.Replacements{"darwin": "macOS"},
			},
		},
	}, nil)

	// Both the definition's own entries, and the entry every version falls through
	// to, which inherits the base the way v1's top level did.
	if len(cfg.VersionOverrides) != 3 {
		t.Fatalf("got %d overrides:\n%+v", len(cfg.VersionOverrides), cfg.VersionOverrides)
	}
	if got := cfg.VersionOverrides[2].VersionConstraints; got != "true" {
		t.Errorf("the last entry is %q, want the one everything matches", got)
	}
}

// The catch-all constraint has to be an expression that evaluates, not a string
// that looks like one.
//
// aqua parses a version_constraint as an expression and requires a boolean out of
// it. Writing it as `"true"` produced "expected bool, but got string", so the
// override matched nothing and the package it belonged to resolved for no version
// at all — a definition that reads as though it covers everything and covers
// nothing.
func TestConfig_catchAllEvaluates(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "cli",
		RepoName:  "cli",
	}, nil)
	if len(cfg.VersionOverrides) != 1 {
		t.Fatalf("got %d overrides, want one", len(cfg.VersionOverrides))
	}

	got, err := expr.EvaluateVersionConstraints(
		slog.New(slog.DiscardHandler), cfg.VersionOverrides[0].VersionConstraints, "v1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("the catch-all constraint doesn't evaluate: %v", err)
	}
	if !got {
		t.Error("the catch-all constraint didn't match")
	}
}

// v1 evaluates the top level before any override and uses it when it matches, so a
// top level with a real constraint is a candidate like any other.
//
// BurntSushi/xsv bounds its top level at ">= 0.10.3" and keeps musl in an override
// for everything from 0.10.0. Dropping the top level sent 0.10.3 to that override,
// which looks for a musl build the newer releases don't have.
func TestConfig_topLevelIsACandidate(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "BurntSushi",
		RepoName:           "xsv",
		VersionConstraints: `semver(">= 0.10.3")`,
		Replacements:       aquaregistry.Replacements{"linux": "unknown-linux-musl"},
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver(">= 0.10.0")`, Replacements: aquaregistry.Replacements{"linux": "unknown-linux-gnu"}},
			{VersionConstraints: `semver("< 0.10.0")`, Replacements: aquaregistry.Replacements{"linux": "linux"}},
		},
	}, nil)

	if got := cfg.VersionOverrides[0].VersionConstraints; got != `semver(">= 0.10.3")` {
		t.Fatalf("the first entry is %q, want the top level's own constraint", got)
	}
	// It carries nothing, because an override inherits the base, which is what the
	// top level was.
	resolved, err := cfg.SetVersion(slog.New(slog.DiscardHandler), "v0.10.3")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Replacements["linux"]; got != "unknown-linux-musl" {
		t.Errorf("0.10.3 resolved to %q, want the top level's", got)
	}
}

// A top level constrained to "false" is v1 saying it is not a candidate, which is
// what g2 means by having no top level at all.
func TestConfig_topLevelFalse(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "Arriven",
		RepoName:           "db1000n",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver("<= 1.0.0")`, Files: []*aquaregistry.File{{Name: "old"}}},
			{VersionConstraints: "true", Files: []*aquaregistry.File{{Name: "new"}}},
		},
	}, nil)
	for _, vo := range cfg.VersionOverrides {
		if vo.VersionConstraints == "false" {
			t.Errorf("the top level came back as a candidate:\n%+v", cfg.VersionOverrides)
		}
	}
}

// v1 reads the top level's constraint first and, when it has none, stops there: the
// overrides are never consulted.
//
// XcodesOrg/xcodes has one saying no_asset for a single version, which has never
// applied to anything. Carried over it would become the entry every version falls
// back to, and the package would resolve to no asset at all.
func TestConfig_unreachableOverrides(t *testing.T) {
	t.Parallel()
	no := true
	cfg, unconverted := migrate.Config(&aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "XcodesOrg",
		RepoName:  "xcodes",
		Asset:     "xcodes.zip",
		// No top-level version_constraint.
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `Version == "1.4.0"`, NoAsset: &no},
		},
	}, nil)

	if len(cfg.VersionOverrides) != 1 {
		t.Fatalf("got %d overrides, want the one that catches everything", len(cfg.VersionOverrides))
	}
	if got := cfg.VersionOverrides[0].VersionConstraints; got != "true" {
		t.Errorf("the constraint is %q, want the one that always matches", got)
	}
	if cfg.VersionOverrides[0].NoAsset != nil {
		t.Error("an override that never applied became the one every version falls back to")
	}
	// Someone wrote it meaning it to work, so it is reported rather than dropped
	// quietly.
	if len(unconverted) != 1 {
		t.Errorf("the unreachable override wasn't reported: %v", unconverted)
	}
}

// A list ending in a constraint every version matches never reaches the top level,
// so what it catches is every tag the repository has — including the ones that are
// no version of the package at all.
//
// superradcompany/microsandbox publishes another component's tags in the same
// repository, and monocore-v0.2.1 parses as no version, so it took that last entry.
// The conversion gives the entry a bound, which stops it catching them, and the
// entry that answers instead has to say what it said.
func TestConfig_fallbackCarriesTheCatchAll(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "superradcompany",
		RepoName:           "microsandbox",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver("<= 0.5.10")`, Replacements: aquaregistry.Replacements{"arm64": "aarch64"}},
			{VersionConstraints: "true", Replacements: aquaregistry.Replacements{"amd64": "x86_64", "arm64": "aarch64"}},
		},
	}, nil)

	resolved, err := cfg.SetVersion(slog.New(slog.DiscardHandler), "monocore-v0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Replacements["amd64"]; got != "x86_64" {
		t.Errorf("a tag that is no version resolved to %q, want what the catch-all said", got)
	}
}

// A list ending in a bound does reach the top level, so the entry that answers for
// what it doesn't match carries nothing and inherits the base.
func TestConfig_fallbackInheritsTheBase(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "bazelbuild",
		RepoName:           "bazel-watcher",
		VersionConstraints: "false",
		Replacements:       aquaregistry.Replacements{"darwin": "Darwin"},
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver("<= 0.4.0")`, Replacements: aquaregistry.Replacements{"darwin": "old"}},
		},
	}, nil)

	resolved, err := cfg.SetVersion(slog.New(slog.DiscardHandler), "V0.26.9")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Replacements["darwin"]; got != "Darwin" {
		t.Errorf("a version that matches nothing resolved to %q, want the base's", got)
	}
}
