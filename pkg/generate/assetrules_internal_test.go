package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

func assetOf(t *testing.T, reg *Registry, env string) *Asset {
	t.Helper()
	for _, a := range reg.Assets {
		if a.OS+"/"+a.Arch == env {
			return a
		}
	}
	return nil
}

// picked returns the asset each override the rules added names, by environment.
func picked(p *aquaregistry.PackageInfo) map[string]string {
	got := map[string]string{}
	for _, ov := range p.Overrides {
		got[ov.GOOS+"/"+ov.GOArch] = ov.Asset
	}
	return got
}

// koalaman/shellcheck's Windows build is shellcheck-<version>.zip, whose name says no OS
// and is the only one of the release's that doesn't. The rule needs no filter, and the
// definition's files apply to the asset it gives windows/amd64.
func TestApplyAssetRules_shellcheck(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "koalaman", RepoName: "shellcheck",
		Files: []*aquaregistry.File{{Name: "shellcheck", Src: "shellcheck-{{.Version}}/shellcheck"}},
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Files: []*aquaregistry.File{{Name: "shellcheck"}}},
		},
	}
	inferred := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "koalaman", RepoName: "shellcheck",
		Asset:         "shellcheck-{{.Version}}.{{.OS}}.{{.Arch}}.{{.Format}}",
		Format:        "tar.xz",
		Replacements:  map[string]string{"amd64": "x86_64", "arm64": "aarch64"},
		SupportedEnvs: aquaregistry.SupportedEnvs{"linux", "darwin"},
	}
	merged := merge(inferred, base)
	err := applyAssetRules(discardLogger(), merged, []*g2.AssetRule{{Envs: []string{"windows/amd64"}}}, &assetRelease{
		names: names(
			"shellcheck-v0.11.0.zip",
			"shellcheck-v0.11.0.linux.x86_64.tar.xz", "shellcheck-v0.11.0.linux.armv6hf.tar.xz",
			"shellcheck-v0.11.0.darwin.aarch64.tar.xz", "shellcheck-v0.11.0.darwin.x86_64.tar.gz",
		),
		version: "v0.11.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := resolve(discardLogger(), "koalaman/shellcheck", merged, base, "v0.11.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	checkShellcheck(t, reg)
}

func checkShellcheck(t *testing.T, reg *Registry) {
	t.Helper()
	windows := assetOf(t, reg, "windows/amd64")
	if windows == nil || windows.Asset != "shellcheck-v0.11.0.zip" || windows.Format != "zip" {
		t.Fatalf("windows/amd64 is %+v, want shellcheck-v0.11.0.zip (zip)", windows)
	}
	if len(windows.Files) != 1 || windows.Files[0].Src != "shellcheck.exe" {
		t.Errorf("windows/amd64 files are %+v, want shellcheck.exe", windows.Files)
	}
	if a := assetOf(t, reg, "windows/arm64"); a != nil {
		t.Errorf("windows/arm64 isn't in the rule: %+v", a)
	}
	if linux := assetOf(t, reg, "linux/amd64"); linux == nil || linux.Asset != "shellcheck-v0.11.0.linux.x86_64.tar.xz" {
		t.Errorf("linux/amd64 is %+v, want the inferred asset", linux)
	}
}

// ip7z/7zip's console binaries for both Windows architectures are in one archive, beside
// the sources and the LZMA SDK, which say no OS either. The filter tells them apart, and
// the definition says the format the inference doesn't know and where each binary is.
func TestApplyAssetRules_7zip(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "ip7z", RepoName: "7zip",
		Files: []*aquaregistry.File{{Name: "7zz"}},
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", GOArch: "amd64", Format: "7z", Files: []*aquaregistry.File{{Name: "7za", Src: "x64/7za.exe"}}},
			{GOOS: "windows", GOArch: "arm64", Format: "7z", Files: []*aquaregistry.File{{Name: "7za", Src: "arm64/7za.exe"}}},
		},
	}
	inferred := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "ip7z", RepoName: "7zip",
		Asset:         "7z{{trimV .Version}}-{{.OS}}-{{.Arch}}.{{.Format}}",
		Format:        "tar.xz",
		SupportedEnvs: aquaregistry.SupportedEnvs{"linux"},
	}
	merged := merge(inferred, base)
	if err := applyAssetRules(discardLogger(), merged, []*g2.AssetRule{
		{Envs: []string{"windows/amd64", "windows/arm64"}, Filter: `Asset contains "extra"`},
	}, &assetRelease{
		names: names(
			"7z2604-extra.7z", "7z2604-src.7z", "7z2604-src.tar.xz", "lzma2604.7z",
			"7z2604.exe", "7z2604.msi", "7z2604-linux-x64.tar.xz", "7z2604-mac.tar.xz",
		),
		version: "26.04",
		filter:  `not (Asset endsWith ".exe") and not (Asset endsWith ".msi")`,
	}); err != nil {
		t.Fatal(err)
	}
	reg, err := resolve(discardLogger(), "ip7z/7zip", merged, base, "26.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	for env, src := range map[string]string{"windows/amd64": "x64/7za.exe", "windows/arm64": "arm64/7za.exe"} {
		a := assetOf(t, reg, env)
		if a == nil || a.Asset != "7z2604-extra.7z" || a.Format != "7z" {
			t.Errorf("%s is %+v, want 7z2604-extra.7z (7z)", env, a)
			continue
		}
		if len(a.Files) != 1 || a.Files[0].Src != src {
			t.Errorf("%s files are %+v, want %s", env, a.Files, src)
		}
	}
}

// The candidates are the assets the all_assets_filter keeps and whose name says no OS, in
// the spellings the definition gives too; the inference picks each environment's among
// them by the architecture the name says, and a name saying none is for every
// architecture of the rule.
func TestApplyAssetRules_candidates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rel  *assetRelease
		want map[string]string
	}{
		{
			name: "by architecture",
			rel:  &assetRelease{names: names("tool-v1.0.0-x64.zip", "tool-v1.0.0-arm64.zip", "tool-v1.0.0-linux-x64.tar.gz")},
			want: map[string]string{"windows/amd64": "tool-v1.0.0-x64.zip", "windows/arm64": "tool-v1.0.0-arm64.zip"},
		},
		{
			name: "no architecture",
			rel:  &assetRelease{names: names("tool-v1.0.0.zip", "tool-v1.0.0.zip.sha256", "tool-v1.0.0-darwin.zip")},
			want: map[string]string{"windows/amd64": "tool-v1.0.0.zip", "windows/arm64": "tool-v1.0.0.zip"},
		},
		{
			name: "all_assets_filter",
			rel:  &assetRelease{names: names("tool-v1.0.0.zip", "tool-v1.0.0.tar.gz"), filter: `not (Asset endsWith ".zip")`},
			want: map[string]string{"windows/amd64": "tool-v1.0.0.tar.gz", "windows/arm64": "tool-v1.0.0.tar.gz"},
		},
		{
			name: "spelling",
			rel:  &assetRelease{names: names("tool-v1.0.0-ubuntu.zip"), spellings: map[string]string{"linux": "ubuntu"}},
			want: map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.rel.version = "v1.0.0"
			p := &aquaregistry.PackageInfo{}
			if err := applyAssetRules(discardLogger(), p, []*g2.AssetRule{{Envs: []string{"windows/amd64", "windows/arm64"}}}, tc.rel); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, picked(p)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// A rule leaving nothing for an environment leaves the inference alone.
func TestApplyAssetRules_noMatch(t *testing.T) {
	t.Parallel()
	p := &aquaregistry.PackageInfo{SupportedEnvs: aquaregistry.SupportedEnvs{"linux"}}
	rules := []*g2.AssetRule{{Envs: []string{"windows/amd64"}, Filter: `Asset endsWith ".zip"`}}
	if err := applyAssetRules(discardLogger(), p, rules, &assetRelease{names: names("tool.tar.gz"), version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if len(p.Overrides) != 0 || len(p.SupportedEnvs) != 1 {
		t.Errorf("a rule matching nothing changed the definition: %+v", p)
	}
}

// The all_assets_filter the candidates are read through is the one answering for the
// version, as aqua gr reads asset_filters.
func TestAssetFilterFor(t *testing.T) {
	t.Parallel()
	cfg := &g2.Config{
		AllAssetsFilter: "top",
		AssetFilters: []*g2.AssetFilter{
			{VersionConstraint: `semver(">= 2.0.0")`, AllAssetsFilter: "new"},
			{VersionConstraint: `Version == "v1.5.0"`},
		},
	}
	for version, want := range map[string]string{"v2.1.0": "new", "v1.5.0": "", "v1.0.0": "top"} {
		if got := assetFilterFor(discardLogger(), cfg, "", version); got != want {
			t.Errorf("%s: got %q, want %q", version, got, want)
		}
	}
}
