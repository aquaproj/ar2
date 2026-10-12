package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/google/go-cmp/cmp"
)

func constraintsOf(overrides []*aquaregistry.VersionOverride) []string {
	out := make([]string, len(overrides))
	for i, vo := range overrides {
		out[i] = vo.VersionConstraints
	}
	return out
}

func cosignSig() *aquaregistry.Cosign {
	return &aquaregistry.Cosign{Opts: []string{"--signature", "{{.Asset}}.sig"}}
}

// loeffel-io/ls-lint's top level and its newest override named different assets in
// aqua-registry. With the asset names left to the release, both come out empty, and the
// newer bound answers the same as the older one would.
func TestConfig_redundantBound(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("loeffel-io/ls-lint", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "loeffel-io",
		RepoName:           "ls-lint",
		Asset:              "ls-lint-{{.OS}}-{{.Arch}}",
		Format:             "raw",
		VersionConstraints: `semver(">= 2.0.0")`,
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver(">= 1.10.0-beta.1")`, Asset: "ls-lint-{{.OS}}", Format: "raw"},
			{VersionConstraints: `semver(">= 1.1.0")`, Asset: "ls-lint-{{.OS}}", Format: "raw", SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "amd64"}},
		},
	}, nil)

	want := []string{`semver(">= 1.10.0-beta.1")`, `semver(">= 1.1.0")`, "true"}
	if diff := cmp.Diff(want, constraintsOf(cfg.VersionOverrides)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
}

// stackrox/kube-linter's entry for v0.6.8 carries the cosign of the range v0.6.8 falls
// in, once the asset name it existed for is left to the release.
func TestConfig_redundantNamedVersion(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("stackrox/kube-linter", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "stackrox",
		RepoName:           "kube-linter",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `Version == "v0.6.8"`, Asset: "kube-linter-{{.OS}}", Format: "raw", Cosign: cosignSig()},
			{VersionConstraints: `semver("<= 0.6.7") && Version != "v0.0.1"`, Cosign: cosignSig(), SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "amd64"}},
			{VersionConstraints: `semver("<= 0.7.6")`, Cosign: cosignSig()},
			{VersionConstraints: "true"},
		},
	}, nil)

	want := []string{`semver("<= 0.6.7") && Version != "v0.0.1"`, `semver("<= 0.7.6")`, "true"}
	if diff := cmp.Diff(want, constraintsOf(cfg.VersionOverrides)); diff != "" {
		t.Errorf("the constraints are wrong (-want +got):\n%s", diff)
	}
}

// An entry naming a version stays when the entry the version would fall to says
// something else.
func TestConfig_namedVersionThatDiffers(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("o/r", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "o",
		RepoName:           "r",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `Version == "v0.6.8"`, NoAsset: new(true)},
			{VersionConstraints: `semver("<= 0.6.7") && Version != "v0.0.1"`, Cosign: cosignSig()},
			{VersionConstraints: `semver("<= 0.7.6")`, Cosign: cosignSig()},
			{VersionConstraints: "true"},
		},
	}, nil)

	if got := cfg.VersionOverrides[0].VersionConstraints; got != `Version == "v0.6.8"` {
		t.Errorf("the entry for v0.6.8 was dropped: %v", constraintsOf(cfg.VersionOverrides))
	}
}

// A bound carrying a prerelease is left alone: whether a prerelease of another version
// satisfies the bound below it is the semver library's to say.
func TestConfig_prereleaseBoundStays(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("o/r", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "o",
		RepoName:           "r",
		Asset:              "r-{{.OS}}-{{.Arch}}",
		Format:             "raw",
		VersionConstraints: `semver(">= 2.0.0-beta.1")`,
		VersionOverrides: []*aquaregistry.VersionOverride{
			{VersionConstraints: `semver(">= 1.0.0")`, Asset: "r-{{.OS}}", Format: "raw"},
			{VersionConstraints: `semver("< 1.0.0")`, NoAsset: new(true)},
		},
	}, nil)

	if got := cfg.VersionOverrides[0].VersionConstraints; got != `semver(">= 2.0.0-beta.1")` {
		t.Errorf("the prerelease bound was dropped: %v", constraintsOf(cfg.VersionOverrides))
	}
}
