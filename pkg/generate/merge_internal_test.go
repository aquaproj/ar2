package generate

import (
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/migrate"
)

func workflow(s string) *aquaregistry.GitHubArtifactAttestations {
	return &aquaregistry.GitHubArtifactAttestations{SignerWorkflow2: s}
}

// One environment's asset can be signed by another workflow than the rest, and the
// entry for it has to say so: FiloSottile/age's v1.3.1 darwin/amd64 asset was signed by
// a workflow that backfilled it.
func TestOverrideSigning(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{OS: "darwin", Arch: "amd64", GitHubArtifactAttestations: workflow("FiloSottile/age/.github/workflows/build.yml")},
		{OS: "darwin", Arch: "arm64", GitHubArtifactAttestations: workflow("FiloSottile/age/.github/workflows/build.yml")},
	}}
	overrideSigning(reg, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{
				GOOS:                       "darwin",
				GOArch:                     "amd64",
				GitHubArtifactAttestations: workflow("FiloSottile/age/.github/workflows/darwin-amd64-backfill.yml"),
			},
		},
	})

	if got := reg.Assets[0].GitHubArtifactAttestations.SignerWorkflow(); got != "FiloSottile/age/.github/workflows/darwin-amd64-backfill.yml" {
		t.Errorf("the overridden environment keeps the wrong workflow: %s", got)
	}
	if got := reg.Assets[1].GitHubArtifactAttestations.SignerWorkflow(); got != "FiloSottile/age/.github/workflows/build.yml" {
		t.Errorf("the other environment was changed: %s", got)
	}
}

// An override selecting on a variant describes a build, not a signature, and the libc
// an entry needs isn't known yet when this runs.
func TestOverrideSigning_variant(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{OS: "linux", Arch: "amd64", GitHubArtifactAttestations: workflow("o/r/.github/workflows/build.yml")},
	}}
	overrideSigning(reg, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{
				GOOS:                       "linux",
				Variants:                   []*aquaregistry.Variant{{Key: "libc", Value: "musl"}},
				GitHubArtifactAttestations: workflow("o/r/.github/workflows/musl.yml"),
			},
		},
	})

	if got := reg.Assets[0].GitHubArtifactAttestations.SignerWorkflow(); got != "o/r/.github/workflows/build.yml" {
		t.Errorf("a variant override was taken for a signature: %s", got)
	}
}

// An override that says nothing about signing leaves the entry alone. Every other
// difference between environments is read off the release.
func TestOverrideSigning_nothingToSay(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{OS: "windows", Arch: "amd64", GitHubArtifactAttestations: workflow("o/r/.github/workflows/build.yml")},
	}}
	overrideSigning(reg, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{{GOOS: "windows", Asset: "r_{{.OS}}.zip"}},
	})

	if got := reg.Assets[0].GitHubArtifactAttestations.SignerWorkflow(); got != "o/r/.github/workflows/build.yml" {
		t.Errorf("the entry was changed: %s", got)
	}
	if reg.Assets[0].Cosign != nil {
		t.Error("the entry gained a signature")
	}
}

// aqua gr infers SLSA provenance from an asset ending in .intoto.jsonl, and can't read
// whether slsa-verifier will accept what is in it. A definition saying it won't is what
// somebody found out about the release.
func TestMerge_slsaProvenance(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		SLSAProvenance: &aquaregistry.SLSAProvenance{
			Type:  "github_release",
			Asset: new("containerd-{{trimV .Version}}-attestation.intoto.jsonl"),
		},
	}
	got := merge(inferred, &aquaregistry.PackageInfo{
		SLSAProvenance: &aquaregistry.SLSAProvenance{Enabled: new(false)},
	})
	if got.SLSAProvenance.GetEnabled() {
		t.Error("the definition says the provenance can't be verified and the inference kept the claim")
	}
	if inferred.SLSAProvenance.GetEnabled() != true {
		t.Error("the inferred definition was modified")
	}
}

// tamasfe/taplo publishes its Windows build as a .gz and as a .zip, and the definition's
// files describe the .zip. The format goes with them, over what was inferred for Windows.
func TestMerge_formatWithFiles(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		Asset:  "taplo-{{.OS}}-{{.Arch}}.{{.Format}}",
		Format: "gz",
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Replacements: map[string]string{"amd64": "x86_64"}},
		},
	}
	got := merge(inferred, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Format: "zip", Files: []*aquaregistry.File{{Name: "taplo", Src: "taplo.exe"}}},
			{GOOS: "darwin", Format: "tar.gz"},
		},
	})
	if len(got.Overrides) != 2 {
		t.Fatalf("got %d overrides, want the definition's format in front of the inferred one:\n%+v", len(got.Overrides), got.Overrides)
	}
	first := got.Overrides[0]
	if first.GOOS != "windows" || first.Format != "zip" {
		t.Errorf("the first override is %+v, want windows with zip", first)
	}
	if first.Replacements["amd64"] != "x86_64" {
		t.Errorf("the inferred override's replacements were lost: %+v", first.Replacements)
	}
	if len(first.Files) != 0 {
		t.Errorf("the files went into the override, which takes them from the definition: %+v", first.Files)
	}
	if inferred.Overrides[0].Format != "" {
		t.Error("the inferred definition was modified")
	}
}

// sass/dart-sass's Windows build runs from a .bat, which no asset name says.
func TestMerge_windowsExt(t *testing.T) {
	t.Parallel()
	got := merge(&aquaregistry.PackageInfo{}, &aquaregistry.PackageInfo{WindowsExt: ".bat"})
	if got.WindowsExt != ".bat" {
		t.Errorf("windows_ext is %q, want .bat", got.WindowsExt)
	}
}

// A platform the inference found no asset for gets the one the definition names, and a
// platform it did find one for keeps the inferred one.
func TestMerge_unnamedOSAsset(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		Asset:         "shellcheck-{{.Version}}.{{.OS}}.{{.Arch}}.{{.Format}}",
		Format:        "tar.xz",
		SupportedEnvs: aquaregistry.SupportedEnvs{"linux", "darwin"},
	}
	got := merge(inferred, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Asset: "shellcheck-{{.Version}}.{{.Format}}", Format: "zip"},
			{GOOS: "linux", Asset: "shellcheck-{{.Version}}.zip", Format: "zip"},
		},
	})
	if len(got.Overrides) != 1 || got.Overrides[0].GOOS != "windows" || got.Overrides[0].Asset != "shellcheck-{{.Version}}.{{.Format}}" {
		t.Errorf("the overrides are %+v, want the Windows one only", got.Overrides)
	}
	if !got.CheckSupportedEnvs("windows", "amd64", "windows/amd64") {
		t.Errorf("windows isn't supported: %v", got.SupportedEnvs)
	}
	if len(inferred.Overrides) != 0 || len(inferred.SupportedEnvs) != 2 {
		t.Error("the inferred definition was modified")
	}
}

func TestAddUnnamedOSAssets(t *testing.T) {
	t.Parallel()
	windows := &aquaregistry.Override{GOOS: "windows", Asset: "tool-{{.Version}}.zip"}
	for _, tt := range []struct {
		name      string
		inferred  aquaregistry.SupportedEnvs
		base      *aquaregistry.PackageInfo
		supported []string
		missing   []string
		format    string
	}{
		{
			// The asset says no architecture, so it is taken for amd64 only.
			name:      "no architecture is amd64",
			inferred:  aquaregistry.SupportedEnvs{"linux", "darwin"},
			base:      &aquaregistry.PackageInfo{Format: "zip", Overrides: []*aquaregistry.Override{windows}},
			supported: []string{"windows/amd64"},
			missing:   []string{"windows/arm64"},
			format:    "zip",
		},
		{
			// The inference read the arm64 build's name and not the amd64 one's.
			name:      "one architecture inferred",
			inferred:  aquaregistry.SupportedEnvs{"linux", "darwin", "windows/arm64"},
			base:      &aquaregistry.PackageInfo{Overrides: []*aquaregistry.Override{windows}},
			supported: []string{"windows/amd64", "windows/arm64"},
		},
		{
			// An asset naming its OS is the inference's to read.
			name:     "the name says the OS",
			inferred: aquaregistry.SupportedEnvs{"linux"},
			base: &aquaregistry.PackageInfo{Overrides: []*aquaregistry.Override{
				{GOOS: "darwin", Asset: "tool-{{.Version}}-macos.tar.gz"},
			}},
			missing: []string{"darwin/amd64"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &aquaregistry.PackageInfo{Format: "tar.gz", SupportedEnvs: tt.inferred}
			addUnnamedOSAssets(p, tt.base)
			for _, env := range tt.supported {
				goos, goarch, _ := strings.Cut(env, "/")
				if !p.CheckSupportedEnvs(goos, goarch, env) {
					t.Errorf("%s isn't supported: %v", env, p.SupportedEnvs)
				}
			}
			for _, env := range tt.missing {
				goos, goarch, _ := strings.Cut(env, "/")
				if p.CheckSupportedEnvs(goos, goarch, env) {
					t.Errorf("%s is supported: %v", env, p.SupportedEnvs)
				}
			}
			if tt.format != "" && (len(p.Overrides) == 0 || p.Overrides[0].Format != tt.format) {
				t.Errorf("the overrides are %+v, want format %s from the definition", p.Overrides, tt.format)
			}
		})
	}
}

// When the inference covers every platform, nothing is added.
func TestAddUnnamedOSAssets_everyPlatform(t *testing.T) {
	t.Parallel()
	p := &aquaregistry.PackageInfo{}
	addUnnamedOSAssets(p, &aquaregistry.PackageInfo{Overrides: []*aquaregistry.Override{
		{GOOS: "windows", Asset: "tool-{{.Version}}.zip"},
	}})
	if len(p.Overrides) != 0 || p.SupportedEnvs != nil {
		t.Errorf("got %+v", p)
	}
}

// The override added for a platform goes after a variant override for it, which aqua
// applies first.
func TestMerge_unnamedOSAssetAfterVariants(t *testing.T) {
	t.Parallel()
	musl := &aquaregistry.Override{GOOS: "linux", Asset: "tool-musl-{{.Version}}.tar.gz", Variants: []*aquaregistry.Variant{{Key: "libc", Value: "musl"}}}
	got := merge(&aquaregistry.PackageInfo{SupportedEnvs: aquaregistry.SupportedEnvs{"darwin"}}, &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{GOOS: "linux", Asset: "tool-{{.Version}}.tar.gz"},
			musl,
		},
	})
	if len(got.Overrides) != 2 || got.Overrides[0] != musl || got.Overrides[1].Asset != "tool-{{.Version}}.tar.gz" {
		t.Errorf("the overrides are %+v", got.Overrides)
	}
}

// koalaman/shellcheck from aqua-registry's definition to the resolved entries: the Windows
// build, shellcheck-<version>.zip, says no OS, and the definition is what gives it to
// Windows, ahead of the format override the same Windows override makes.
func TestUnnamedOSAsset_shellcheck(t *testing.T) {
	t.Parallel()
	g1 := &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "koalaman",
		RepoName:           "shellcheck",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{
				VersionConstraints: "true",
				Asset:              "shellcheck-{{.Version}}.{{.OS}}.{{.Arch}}.{{.Format}}",
				Format:             "tar.xz",
				Files:              []*aquaregistry.File{{Name: "shellcheck", Src: "shellcheck-{{.Version}}/shellcheck"}},
				Replacements:       map[string]string{"amd64": "x86_64", "arm64": "aarch64"},
				Overrides: []*aquaregistry.Override{
					{
						GOOS:   "windows",
						Asset:  "shellcheck-{{.Version}}.{{.Format}}",
						Format: "zip",
						Files:  []*aquaregistry.File{{Name: "shellcheck"}},
					},
				},
			},
		},
	}
	cfg, _ := migrate.Config("koalaman/shellcheck", g1, nil)
	base, err := cfg.SetVersion(discardLogger(), "v0.11.0")
	if err != nil {
		t.Fatal(err)
	}
	// What aqua gr infers from v0.11.0's assets: nothing for Windows.
	inferred := &aquaregistry.PackageInfo{
		Type:          "github_release",
		RepoOwner:     "koalaman",
		RepoName:      "shellcheck",
		Asset:         "shellcheck-{{.Version}}.{{.OS}}.{{.Arch}}.{{.Format}}",
		Format:        "tar.xz",
		Replacements:  map[string]string{"amd64": "x86_64", "arm64": "aarch64"},
		SupportedEnvs: aquaregistry.SupportedEnvs{"linux", "darwin"},
	}
	reg, err := resolve(discardLogger(), "koalaman/shellcheck", merge(inferred, base), base, "v0.11.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := assetFor(reg, "windows", "arm64"); a != nil {
		t.Errorf("windows/arm64 got the amd64 build without emulation: %+v", a)
	}
	windows := assetFor(reg, "windows", "amd64")
	if windows == nil {
		t.Fatal("no windows/amd64 entry")
	}
	if windows.Asset != "shellcheck-v0.11.0.zip" || windows.Format != "zip" {
		t.Errorf("windows/amd64 is %s (%s), want shellcheck-v0.11.0.zip (zip)", windows.Asset, windows.Format)
	}
	if len(windows.Files) != 1 || windows.Files[0].Src != "shellcheck.exe" {
		t.Errorf("windows/amd64 files are %+v, want shellcheck.exe", windows.Files)
	}
}

func assetFor(reg *Registry, goos, goarch string) *Asset {
	for _, a := range reg.Assets {
		if a.OS == goos && a.Arch == goarch {
			return a
		}
	}
	return nil
}

// An asset whose name is built from the architecture is for both of them.
func TestAddUnnamedOSAssets_arch(t *testing.T) {
	t.Parallel()
	p := &aquaregistry.PackageInfo{SupportedEnvs: aquaregistry.SupportedEnvs{"linux"}}
	addUnnamedOSAssets(p, &aquaregistry.PackageInfo{Overrides: []*aquaregistry.Override{
		{GOOS: "windows", Asset: "tool-{{.Version}}-{{.Arch}}.zip", Format: "zip"},
	}})
	for _, goarch := range []string{"amd64", "arm64"} {
		if !p.CheckSupportedEnvs("windows", goarch, "windows/"+goarch) {
			t.Errorf("windows/%s isn't supported: %v", goarch, p.SupportedEnvs)
		}
	}
}
