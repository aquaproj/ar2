package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
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
