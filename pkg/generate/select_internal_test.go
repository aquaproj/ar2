package generate

import (
	"errors"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// A release publishing a glibc and a musl build of a platform gets an entry for each, told
// apart by the libc its name says.
func TestSelectAssets_libc(t *testing.T) {
	t.Parallel()
	input := &Input{PkgName: "foo/foo", Version: "v1.0.0"}
	got := assetsByEnv(selected(t, input, nil,
		"foo-x86_64-unknown-linux-gnu.tar.gz", "foo-x86_64-unknown-linux-musl.tar.gz",
		"foo-aarch64-apple-darwin.tar.gz",
	))
	want := map[string]string{
		"darwin/arm64":      "foo-aarch64-apple-darwin.tar.gz",
		"linux/amd64/glibc": "foo-x86_64-unknown-linux-gnu.tar.gz",
		"linux/amd64/musl":  "foo-x86_64-unknown-linux-musl.tar.gz",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// Assets of one environment that nothing tells apart fail the version, naming them, for a
// filter to settle.
func TestSelectAssets_tie(t *testing.T) {
	t.Parallel()
	input := &Input{PkgName: "ollama/ollama", Version: "v0.5.0"}
	_, _, err := selectAssets(discardLogger(), input, nil, &release{names: names(
		"ollama-linux-amd64.tgz", "ollama-linux-amd64-rocm.tgz",
	)})
	if !errors.Is(err, errTie) {
		t.Fatalf("got %v, want a tie", err)
	}
}

// tamasfe/taplo publishes its Windows build as a .gz and as a .zip, and the definition's
// files describe the .zip. The format it declares for Windows settles which.
func TestSelectAssets_tieSettledByFormat(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "tamasfe", RepoName: "taplo",
		Format: "gz",
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Format: "zip", Files: []*aquaregistry.File{{Name: "taplo", Src: "taplo.exe"}}},
		},
	}
	reg := selected(t, &Input{PkgName: "tamasfe/taplo", Version: "0.10.0"}, base,
		"taplo-windows-x86_64.gz", "taplo-windows-x86_64.zip", "taplo-linux-x86_64.gz",
	)
	windows := assetOf(t, reg, "windows/amd64")
	if windows == nil || windows.Asset != "taplo-windows-x86_64.zip" || windows.Format != "zip" {
		t.Fatalf("windows/amd64 is %+v, want taplo-windows-x86_64.zip (zip)", windows)
	}
	if len(windows.Files) != 1 || windows.Files[0].Src != "taplo.exe" {
		t.Errorf("windows/amd64 files are %+v, want taplo.exe", windows.Files)
	}
}

// sass/dart-sass's Windows build runs from a .bat, which no asset name says.
func TestSelectAssets_windowsExt(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "sass", RepoName: "dart-sass",
		WindowsExt: ".bat",
		Files:      []*aquaregistry.File{{Name: "sass", Src: "dart-sass/sass"}},
	}
	reg := selected(t, &Input{PkgName: "sass/dart-sass", Version: "1.80.0"}, base,
		"dart-sass-1.80.0-windows-x64.zip",
	)
	windows := assetOf(t, reg, "windows/amd64")
	if windows == nil || len(windows.Files) != 1 || windows.Files[0].Src != "dart-sass/sass.bat" {
		t.Fatalf("windows/amd64 is %+v, want its executable to be dart-sass/sass.bat", windows)
	}
}

// A release holding no asset for any environment generates nothing rather than an empty
// file.
func TestSelectAssets_noAsset(t *testing.T) {
	t.Parallel()
	_, _, err := selectAssets(discardLogger(), &Input{PkgName: "foo/foo", Version: "v1.0.0"}, nil, &release{names: names("README.md", "checksums.txt")})
	if !errors.Is(err, errNoAsset) {
		t.Fatalf("got %v, want no asset", err)
	}
}

// An entry gets its asset's own provenance, or the release's when it has none, and a
// definition saying anything about SLSA provenance wins.
func TestInferSLSA(t *testing.T) {
	t.Parallel()
	entries := func() *Registry {
		return &Registry{Assets: []*Asset{
			{OS: "linux", Arch: "amd64", Type: "github_release", Asset: "foo-linux-amd64.tar.gz"},
			{OS: "darwin", Arch: "arm64", Type: "github_release", Asset: "foo-darwin-arm64.tar.gz"},
		}}
	}
	provenances := func(reg *Registry) map[string]string {
		out := map[string]string{}
		for _, a := range reg.Assets {
			if a.SLSAProvenance != nil {
				out[a.OS+"/"+a.Arch] = *a.SLSAProvenance.Asset
			}
		}
		return out
	}

	reg := entries()
	inferSLSA(reg, nil, names("foo-linux-amd64.tar.gz", "foo-linux-amd64.tar.gz.intoto.jsonl", "foo-darwin-arm64.tar.gz", "multiple.intoto.jsonl"), slsaOnly)
	want := map[string]string{
		"linux/amd64":  "foo-linux-amd64.tar.gz.intoto.jsonl",
		"darwin/arm64": "multiple.intoto.jsonl",
	}
	if diff := cmp.Diff(want, provenances(reg)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	reg = entries()
	inferSLSA(reg, &aquaregistry.PackageInfo{SLSAProvenance: &aquaregistry.SLSAProvenance{Enabled: new(false)}},
		names("foo-linux-amd64.tar.gz.intoto.jsonl", "multiple.intoto.jsonl"), slsaOnly)
	if got := provenances(reg); len(got) != 0 {
		t.Errorf("the definition says the provenance can't be verified and the inference made a claim: %v", got)
	}
}

// kcp-dev/kcp's Windows assets are kubectl plugins that nothing tells apart, and the
// definition doesn't support Windows. They are left out rather than failing the version, and
// the command's own assets are told from the other programs' by its name.
func TestSelectAssets_tieOutsideSupportedEnvs(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "kcp-dev", RepoName: "kcp",
		Files:         []*aquaregistry.File{{Name: "kcp", Src: "bin/kcp"}},
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux"},
	}
	got := assetsByEnv(selected(t, &Input{PkgName: "kcp-dev/kcp", Version: "v0.33.1"}, base,
		"kcp_0.33.1_linux_amd64.tar.gz", "apigen_0.33.1_linux_amd64.tar.gz",
		"kubectl-kcp-plugin_0.33.1_windows_amd64.tar.gz", "kubectl-ws-plugin_0.33.1_windows_amd64.tar.gz",
	))
	if diff := cmp.Diff(map[string]string{"linux/amd64": "kcp_0.33.1_linux_amd64.tar.gz"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// An environment whose assets nothing tells apart is left out, and said, while the ones that
// have an asset keep the version. So is an environment the definition supports that the
// release has nothing for.
func TestSelectAssets_leftOut(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{
		Type: "github_release", RepoOwner: "ollama", RepoName: "ollama",
		SupportedEnvs: aquaregistry.SupportedEnvs{"linux", "darwin"},
	}
	reg, left, err := selectAssets(discardLogger(), &Input{PkgName: "ollama/ollama", Version: "v0.5.0"}, base, &release{names: names(
		"ollama-linux-amd64.tgz", "ollama-linux-amd64-rocm.tgz", "ollama-linux-arm64.tgz",
	)})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]string{"linux/arm64": "ollama-linux-arm64.tgz"}, assetsByEnv(reg)); diff != "" {
		t.Errorf("the file (-want +got):\n%s", diff)
	}
	got := map[string]string{}
	for _, l := range left {
		got[l.Environment()] = l.Reason
	}
	want := map[string]string{
		"linux/amd64":  errTie.Error() + ": ollama-linux-amd64-rocm.tgz, ollama-linux-amd64.tgz",
		"darwin/amd64": errNoAssetForEnv.Error(),
		"darwin/arm64": errNoAssetForEnv.Error(),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("what is left out (-want +got):\n%s", diff)
	}
}
