package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/google/go-cmp/cmp"
)

// A package whose asset names say no OS gets an asset rule for the environments its
// supported_envs names, filtered to what its templates match; one whose templates name the OS
// gets none, and an environment whose template names it is left to the names.
func TestConfig_assetRules(t *testing.T) { //nolint:funlen // a table of definitions
	t.Parallel()
	tests := []struct {
		name    string
		pkgName string
		info    *aquaregistry.PackageInfo
		want    []*g2.AssetRule
	}{
		{
			// opencontainers/runc publishes runc.amd64 beside runc-1.5.2.tar.xz, its source.
			name:    "linux only",
			pkgName: "opencontainers/runc",
			// As aqua-registry has it: everything in its one version_override.
			info: &aquaregistry.PackageInfo{
				Type: "github_release", RepoOwner: "opencontainers", RepoName: "runc",
				VersionConstraints: "false",
				VersionOverrides: []*aquaregistry.VersionOverride{{
					VersionConstraints: "true",
					Asset:              "runc.{{.Arch}}", Format: "raw",
					SupportedEnvs: aquaregistry.SupportedEnvs{"linux"},
				}},
			},
			want: []*g2.AssetRule{{
				Envs:   []string{"linux/amd64", "linux/arm64"},
				Filter: `Asset matches "^runc[.].+([.].+)?$"`,
			}},
		},
		{
			// A jar, which aqua-registry offers on Linux and macOS.
			name:    "the environments supported_envs names",
			pkgName: "trinodb/trino/trino-cli",
			info: &aquaregistry.PackageInfo{
				Type: "github_release", RepoOwner: "trinodb", RepoName: "trino",
				Asset: "trino-cli-{{.Version}}", Format: "raw",
				SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux"},
			},
			want: []*g2.AssetRule{{
				Envs:   []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"},
				Filter: `Asset matches "^trino-cli-.+([.].+)?$"`,
			}},
		},
		{
			// No supported_envs is every environment.
			name:    "every environment",
			pkgName: "foo/bar",
			info: &aquaregistry.PackageInfo{
				Type: "github_release", RepoOwner: "foo", RepoName: "bar",
				Asset: "bar.jar", Format: "raw",
			},
			want: []*g2.AssetRule{{
				Envs: []string{
					"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64",
					"windows/amd64", "windows/arm64",
				},
				Filter: `Asset matches "^bar[.]jar([.].+)?$"`,
			}},
		},
		{
			name:    "the templates name the OS",
			pkgName: "cli/cli",
			info: &aquaregistry.PackageInfo{
				Type: "github_release", RepoOwner: "cli", RepoName: "cli",
				Asset: "gh_{{trimV .Version}}_{{.OS}}_{{.Arch}}.{{.Format}}", Format: "tar.gz",
				Overrides: []*aquaregistry.Override{{GOOS: "darwin", Asset: "gh_{{trimV .Version}}_macOS_{{.Arch}}.zip"}},
			},
		},
		{
			// Only Linux's asset names no OS; macOS's says it in a word.
			name:    "one OS of them",
			pkgName: "foo/foo",
			info: &aquaregistry.PackageInfo{
				Type: "github_release", RepoOwner: "foo", RepoName: "foo",
				Asset:         "foo-{{.Arch}}.tar.gz",
				Overrides:     []*aquaregistry.Override{{GOOS: "darwin", Asset: "foo-macos.zip"}},
				SupportedEnvs: aquaregistry.SupportedEnvs{"linux", "darwin"},
			},
			want: []*g2.AssetRule{{
				Envs:   []string{"linux/amd64", "linux/arm64"},
				Filter: `Asset matches "^foo-.+[.]tar[.]gz([.].+)?$"`,
			}},
		},
		{
			name:    "not a release",
			pkgName: "hashicorp/terraform",
			info: &aquaregistry.PackageInfo{
				Type: "http", URL: "https://releases.hashicorp.com/terraform/{{trimV .Version}}/terraform.zip",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, _ := migrate.Config(tt.pkgName, tt.info, nil)
			if diff := cmp.Diff(tt.want, cfg.AssetRules); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
