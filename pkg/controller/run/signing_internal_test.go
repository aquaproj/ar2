package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/google/go-cmp/cmp"
)

// signed builds an asset carrying the named signing.
func signed(os, arch string, kinds ...string) *generate.Asset {
	a := &generate.Asset{OS: os, Arch: arch}
	for _, kind := range kinds {
		switch kind {
		case "cosign":
			a.Cosign = &aquaregistry.Cosign{Opts: []string{"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com"}}
		case "github_artifact_attestations":
			a.GitHubArtifactAttestations = &aquaregistry.GitHubArtifactAttestations{}
		case "minisign":
			a.Minisign = &aquaregistry.Minisign{}
		case "slsa_provenance":
			a.SLSAProvenance = &aquaregistry.SLSAProvenance{Type: "github_release"}
		}
	}
	return a
}

func registryOf(assets ...*generate.Asset) *aquag2.Registry {
	return &aquag2.Registry{Assets: assets}
}

func TestLostSigning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		older, newer *aquag2.Registry
		want         []string
	}{
		{
			name:  "nothing lost",
			older: registryOf(signed("linux", "amd64", "cosign")),
			newer: registryOf(signed("linux", "amd64", "cosign")),
		},
		{
			name:  "the release stopped signing",
			older: registryOf(signed("linux", "amd64", "cosign", "slsa_provenance")),
			newer: registryOf(signed("linux", "amd64", "cosign")),
			want:  []string{"linux/amd64: slsa_provenance"},
		},
		{
			// One platform losing its signature is a loss even when the others keep
			// theirs: that is one asset an attacker would have had to sign and
			// didn't.
			name: "only one platform lost it",
			older: registryOf(
				signed("linux", "amd64", "github_artifact_attestations"),
				signed("darwin", "arm64", "github_artifact_attestations"),
			),
			newer: registryOf(
				signed("linux", "amd64", "github_artifact_attestations"),
				signed("darwin", "arm64"),
			),
			want: []string{"darwin/arm64: github_artifact_attestations"},
		},
		{
			name:  "gaining one is not a loss",
			older: registryOf(signed("linux", "amd64")),
			newer: registryOf(signed("linux", "amd64", "minisign")),
		},
		{
			// A platform the older version didn't build for has nothing to have
			// lost.
			name:  "a new platform",
			older: registryOf(signed("linux", "amd64", "cosign")),
			newer: registryOf(signed("linux", "amd64", "cosign"), signed("linux", "arm64")),
		},
		{
			name: "variants are told apart",
			older: registryOf(
				withVariants(signed("linux", "amd64", "cosign"), map[string]string{"libc": "musl"}),
				withVariants(signed("linux", "amd64", "cosign"), map[string]string{"libc": "gnu"}),
			),
			newer: registryOf(
				withVariants(signed("linux", "amd64"), map[string]string{"libc": "musl"}),
				withVariants(signed("linux", "amd64", "cosign"), map[string]string{"libc": "gnu"}),
			),
			want: []string{"linux/amd64/libc=musl: cosign"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, lostSigning(tt.older, tt.newer)); diff != "" {
				t.Errorf("what was lost is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

func withVariants(a *generate.Asset, variants map[string]string) *generate.Asset {
	a.Variants = variants
	return a
}

// A signature that is configured but switched off doesn't count as one.
func TestLostSigning_disabled(t *testing.T) {
	t.Parallel()
	no := false
	older := registryOf(&generate.Asset{OS: "linux", Arch: "amd64", Cosign: &aquaregistry.Cosign{Enabled: &no}})
	newer := registryOf(signed("linux", "amd64"))
	if lost := lostSigning(older, newer); len(lost) != 0 {
		t.Errorf("lost %v, want nothing", lost)
	}
}

func TestCheckSigning(t *testing.T) {
	t.Parallel()
	versions := []*version{
		{Version: "v3", Registry: registryOf(signed("linux", "amd64"))},
		{Version: "v2", Registry: registryOf(signed("linux", "amd64", "cosign"))},
	}
	checkSigning(discardLogger(), "cli/cli", versions,
		registryOf(signed("linux", "amd64", "cosign")))

	if !versions[0].NeedsReview {
		t.Error("the version that lost its signature must be left for review")
	}
	if versions[1].NeedsReview {
		t.Error("the version that kept its signature must not be")
	}
	if diff := cmp.Diff([]string{"linux/amd64: cosign"}, versions[0].LostSigning); diff != "" {
		t.Errorf("what was lost is wrong (-want +got):\n%s", diff)
	}
}

// With nothing already in the repository, the oldest version of the run stands in.
// That is what catches the first sweep of a package whose newest release dropped
// what the ones before it had.
func TestCheckSigning_noBaseline(t *testing.T) {
	t.Parallel()
	versions := []*version{
		{Version: "v3", Registry: registryOf(signed("linux", "amd64"))},
		{Version: "v2", Registry: registryOf(signed("linux", "amd64", "cosign"))},
		{Version: "v1", Registry: registryOf(signed("linux", "amd64", "cosign"))},
	}
	checkSigning(discardLogger(), "cli/cli", versions, nil)

	if !versions[0].NeedsReview {
		t.Error("the newest version lost a signature and must be left for review")
	}
	if versions[1].NeedsReview || versions[2].NeedsReview {
		t.Error("the versions that kept theirs must not be")
	}
}

// One version and nothing to compare it against is the ordinary case for a package
// the repository has never held. It is not a reason to send it for review.
func TestCheckSigning_nothingToCompare(t *testing.T) {
	t.Parallel()
	versions := []*version{{Version: "v1", Registry: registryOf(signed("linux", "amd64"))}}
	checkSigning(discardLogger(), "cli/cli", versions, nil)
	if versions[0].NeedsReview {
		t.Error("a single version with no baseline must not be left for review")
	}
}

func TestBaselineVersion(t *testing.T) {
	t.Parallel()
	// The release list is newest first, so the first one the repository holds is the
	// newest one it holds.
	got := baselineVersion([]string{"v5", "v4", "v3", "v2"}, map[string]struct{}{
		"v3": {}, "v2": {},
	})
	if got != "v3" {
		t.Errorf("the baseline is %q, want v3", got)
	}
	if got := baselineVersion([]string{"v2", "v1"}, map[string]struct{}{}); got != "" {
		t.Errorf("a package the repository holds nothing of has no baseline, got %q", got)
	}
}

// The signer read off the release is written into the definition, so that every
// version after this is held to it.
func TestPinSigner(t *testing.T) {
	t.Parallel()
	// What the run generated: the release it looked at, signed under its own tag.
	bundle := "gh_2.0.0_linux_amd64.tar.gz.sigstore.json"
	observed := &aquaregistry.Cosign{
		Bundle: &aquaregistry.DownloadedFile{Type: "github_release", Asset: &bundle},
		Opts: []string{
			"--certificate-identity", "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/tags/v2.0.0",
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
		},
	}
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	pinSigner(discardLogger(), "cli/cli", cfg, []*version{
		{Version: "v2.0.0", Registry: registryOf(&generate.Asset{OS: "linux", Arch: "amd64", Cosign: observed})},
	})

	// The definition applies to every version, so the one that was looked at goes
	// back to being a template.
	want := "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/tags/{{.Version}}"
	if got := cfg.Cosign.Opts[1]; got != want {
		t.Errorf("the identity is %q, want %q", got, want)
	}
	if got := *cfg.Cosign.Bundle.Asset; got != "{{.Asset}}.sigstore.json" {
		t.Errorf("the bundle is %q", got)
	}
	// What the run generated is untouched: it describes one version.
	if observed.Opts[1] != "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/tags/v2.0.0" {
		t.Errorf("the generated file was rewritten: %q", observed.Opts[1])
	}
}

// A definition that already says who signs was written by someone who knows the
// package. Replacing it with whatever signed the release ar2 happened to look at is
// exactly what must not happen: it is the pinned value a later release is checked
// against.
func TestPinSigner_definitionWins(t *testing.T) {
	t.Parallel()
	declared := &aquaregistry.Cosign{Opts: []string{"--certificate-identity", "the reviewed one"}}
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{Cosign: declared}}
	pinSigner(discardLogger(), "cli/cli", cfg, []*version{
		{Version: "v2.0.0", Registry: registryOf(&generate.Asset{
			OS: "linux", Arch: "amd64",
			Cosign: &aquaregistry.Cosign{Opts: []string{"--certificate-identity", "somebody else"}},
		})},
	})
	if cfg.Cosign != declared {
		t.Errorf("the definition's signer was replaced: %+v", cfg.Cosign)
	}
}

// Nothing is recorded from a release whose signature only ever matched a pattern:
// that is the guess, not a name.
func TestPinSigner_noIdentity(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	pinSigner(discardLogger(), "cli/cli", cfg, []*version{
		{Version: "v2.0.0", Registry: registryOf(&generate.Asset{
			OS: "linux", Arch: "amd64",
			Cosign: &aquaregistry.Cosign{Opts: []string{"--certificate-identity-regexp", "^https://github\\.com/cli/.+$"}},
		})},
	})
	if cfg.Cosign != nil {
		t.Errorf("a pattern was recorded as a signer: %+v", cfg.Cosign)
	}
}
