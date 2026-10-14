package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
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

// selected generates the entries of a release holding the given assets.
func selected(t *testing.T, input *Input, base *aquaregistry.PackageInfo, assets ...string) *Registry {
	t.Helper()
	reg, _, err := selectAssets(discardLogger(), input, base, &release{names: names(assets...)})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// assetsByEnv maps each entry's environment to its asset.
func assetsByEnv(reg *Registry) map[string]string {
	got := map[string]string{}
	for _, a := range reg.Assets {
		env := a.OS + "/" + a.Arch
		if libc := a.Variants["libc"]; libc != "" {
			env += "/" + libc
		}
		got[env] = a.Asset
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
	input := &Input{
		PkgName: "koalaman/shellcheck", Version: "v0.11.0",
		Config: &g2.Config{AssetRules: []*g2.AssetRule{{Envs: []string{"windows/amd64"}}}},
	}
	reg := selected(t, input, base,
		"shellcheck-v0.11.0.zip",
		"shellcheck-v0.11.0.linux.x86_64.tar.xz", "shellcheck-v0.11.0.linux.armv6hf.tar.xz",
		"shellcheck-v0.11.0.darwin.aarch64.tar.xz", "shellcheck-v0.11.0.darwin.x86_64.tar.gz",
	)
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
	linux := assetOf(t, reg, "linux/amd64")
	if linux == nil || linux.Asset != "shellcheck-v0.11.0.linux.x86_64.tar.xz" {
		t.Fatalf("linux/amd64 is %+v, want the asset its name places there", linux)
	}
	if diff := cmp.Diff([]*File{{Name: "shellcheck", Src: "shellcheck-v0.11.0/shellcheck"}}, linux.Files); diff != "" {
		t.Errorf("linux/amd64 files aren't the definition's (-want +got):\n%s", diff)
	}
}

// ip7z/7zip's console binaries for both Windows architectures are in one archive, beside
// the sources and the LZMA SDK, which say no OS either. The filter tells them apart, and
// the definition says the format the parser doesn't know and where each binary is.
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
	input := &Input{
		PkgName: "ip7z/7zip", Version: "26.04",
		Scaffold: &genrgst.RawConfig{AllAssetsFilter: `not (Asset endsWith ".exe") and not (Asset endsWith ".msi")`},
		Config: &g2.Config{AssetRules: []*g2.AssetRule{
			{Envs: []string{"windows/amd64", "windows/arm64"}, Filter: `Asset contains "extra"`},
		}},
	}
	reg := selected(t, input, base,
		"7z2604-extra.7z", "7z2604-src.7z", "7z2604-src.tar.xz", "lzma2604.7z",
		"7z2604.exe", "7z2604.msi", "7z2604-linux-x64.tar.xz", "7z2604-mac.tar.xz",
	)
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
// the spellings the definition gives too; each environment's is picked among them by the
// architecture the name says, and a name saying none is for every architecture of the
// rule.
func TestApplyAssetRules_candidates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		assets    []string
		filter    string
		spellings map[string]string
		want      map[string]string
	}{
		{
			name:   "by architecture",
			assets: []string{"tool-v1.0.0-x64.zip", "tool-v1.0.0-arm64.zip", "tool-v1.0.0-linux-x64.tar.gz"},
			want:   map[string]string{"windows/amd64": "tool-v1.0.0-x64.zip", "windows/arm64": "tool-v1.0.0-arm64.zip"},
		},
		{
			name:   "no architecture",
			assets: []string{"tool-v1.0.0.zip", "tool-v1.0.0.zip.sha256", "tool-v1.0.0-darwin.zip"},
			want:   map[string]string{"windows/amd64": "tool-v1.0.0.zip", "windows/arm64": "tool-v1.0.0.zip"},
		},
		{
			name:   "all_assets_filter",
			assets: []string{"tool-v1.0.0.zip", "tool-v1.0.0.tar.gz", "tool-v1.0.0-linux-amd64.tar.gz"},
			filter: `not (Asset endsWith ".zip")`,
			want:   map[string]string{"windows/amd64": "tool-v1.0.0.tar.gz", "windows/arm64": "tool-v1.0.0.tar.gz"},
		},
		{
			name:      "spelling",
			assets:    []string{"tool-v1.0.0-ubuntu.zip"},
			spellings: map[string]string{"linux": "ubuntu"},
			want:      map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := &Input{
				PkgName: "foo/tool", Version: "v1.0.0",
				Scaffold: &genrgst.RawConfig{AllAssetsFilter: tc.filter},
				Config:   &g2.Config{AssetRules: []*g2.AssetRule{{Envs: []string{"windows/amd64", "windows/arm64"}}}},
			}
			base := &aquaregistry.PackageInfo{Type: "github_release", RepoOwner: "foo", RepoName: "tool", Replacements: tc.spellings}
			got := map[string]string{}
			for env, asset := range assetsByEnv(selected(t, input, base, tc.assets...)) {
				if env == "windows/amd64" || env == "windows/arm64" {
					got[env] = asset
				}
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// A rule leaving nothing for an environment leaves the names' answer alone.
func TestApplyAssetRules_noMatch(t *testing.T) {
	t.Parallel()
	input := &Input{
		PkgName: "foo/tool", Version: "v1.0.0",
		Config: &g2.Config{AssetRules: []*g2.AssetRule{{Envs: []string{"windows/amd64"}, Filter: `Asset endsWith ".zip"`}}},
	}
	base := &aquaregistry.PackageInfo{Type: "github_release", RepoOwner: "foo", RepoName: "tool"}
	got := assetsByEnv(selected(t, input, base, "tool.tar.gz", "tool-linux-amd64.tar.gz"))
	if diff := cmp.Diff(map[string]string{"linux/amd64": "tool-linux-amd64.tar.gz"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// The all_assets_filter the assets are read through is the one answering for the version,
// as aqua gr reads asset_filters.
func TestAllAssetsFilter(t *testing.T) {
	t.Parallel()
	scaffold := &genrgst.RawConfig{
		AllAssetsFilter: "top",
		VersionOverrides: rawAssetFilters([]*g2.AssetFilter{
			{VersionConstraint: `semver(">= 2.0.0")`, AllAssetsFilter: "new"},
			{VersionConstraint: `Version == "v1.5.0"`},
		}),
	}
	for version, want := range map[string]string{"v2.1.0": "new", "v1.5.0": "", "v1.0.0": "top"} {
		got, err := allAssetsFilter(discardLogger(), scaffold, "", version)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", version, got, want)
		}
	}
}
