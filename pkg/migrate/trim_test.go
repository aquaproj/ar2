package migrate_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/google/go-cmp/cmp"
)

// A definition says how a release writes a platform, and most of what it says the parser
// works out for itself. What it keeps is the part that can't be worked out.
func TestConfig_replacements(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "luau-lang",
		RepoName:  "luau",
		Replacements: aquaregistry.Replacements{
			"amd64":   "x86_64",
			"arm64":   "aarch64",
			"darwin":  "apple-darwin",
			"windows": "pc-windows-msvc",
			"linux":   "ubuntu",
		},
	}, nil)

	want := aquaregistry.Replacements{"linux": "ubuntu"}
	if diff := cmp.Diff(want, cfg.Replacements); diff != "" {
		t.Errorf("the replacements are wrong (-want +got):\n%s", diff)
	}
}

// A definition saying nothing the parser doesn't know says nothing at all, and the field
// goes rather than being written empty.
func TestConfig_replacementsAllKnown(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:         "github_release",
		RepoOwner:    "astral-sh",
		RepoName:     "uv",
		Replacements: aquaregistry.Replacements{"amd64": "x86_64", "darwin": "apple-darwin"},
	}, nil)

	if cfg.Replacements != nil {
		t.Errorf("the replacements are %+v, want none", cfg.Replacements)
	}
}

// goos, goarch, envs and variants are conditions: they say what an override matches and
// change nothing, so an override carrying only those says nothing here. aqua-registry
// writes one first so that an aqua too old to know variants matches it and stays on the
// asset it always had; nothing reading this registry is that old.
func TestConfig_conditionOnlyOverride(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config(&aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "pnpm",
		RepoName:           "pnpm",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{
				VersionConstraints: "true",
				Asset:              "pnpm-{{.OS}}-{{.Arch}}.{{.Format}}",
				Format:             "tar.gz",
				Files:              []*aquaregistry.File{{Name: "pnpm"}},
				Overrides: []*aquaregistry.Override{
					{GOOS: "linux", Variants: []*aquaregistry.Variant{{Key: "libc", Value: "glibc"}}},
					{
						GOOS:     "linux",
						Asset:    "pnpm-{{.OS}}-{{.Arch}}-musl.{{.Format}}",
						Variants: []*aquaregistry.Variant{{Key: "libc", Value: "musl"}},
					},
				},
			},
		},
	}, nil)

	if len(cfg.VersionOverrides) == 0 {
		t.Fatal("got no version_overrides")
	}
	overrides := cfg.VersionOverrides[0].Overrides
	if len(overrides) != 1 {
		t.Fatalf("got %d overrides, want the one that says something:\n%+v", len(overrides), overrides)
	}
	// And it keeps its asset: which libc a build was made against is in no asset name the
	// parser knows how to read, so without it the override says nothing either.
	if got := overrides[0].Asset; got != "pnpm-{{.OS}}-{{.Arch}}-musl.{{.Format}}" {
		t.Errorf("the asset is %q, want the musl one", got)
	}
	if len(overrides[0].Variants) != 1 || overrides[0].Variants[0].Value != "musl" {
		t.Errorf("the override matches %+v, want the musl variant", overrides[0].Variants)
	}
}

// aqua-registry verifies an asset against the checksum file. A generated entry carries
// the digest of the asset itself and has nowhere to say that the file exists, so a
// definition saying where it is, signature and all, says it to nobody.
func TestConfig_checksumGoes(t *testing.T) {
	t.Parallel()
	signed := &aquaregistry.Checksum{
		Type:      "github_release",
		Asset:     "grype_{{trimV .Version}}_checksums.txt",
		Algorithm: "sha256",
		Cosign:    &aquaregistry.Cosign{Opts: []string{"--certificate-identity-regexp", "anything"}},
	}
	for _, pkgInfo := range []*aquaregistry.PackageInfo{
		{
			Type: "github_release", RepoOwner: "anchore", RepoName: "grype",
			Checksum: signed,
			VersionOverrides: []*aquaregistry.VersionOverride{
				{VersionConstraints: "true", Checksum: signed},
			},
		},
		{
			// An http package keeps everything else, because a release says nothing
			// about a URL. The checksum file is read by nobody whatever the type is.
			Type: "http", URL: "https://example.com/{{.Version}}",
			Checksum: signed,
			VersionOverrides: []*aquaregistry.VersionOverride{
				{VersionConstraints: "true", Checksum: signed},
			},
		},
	} {
		t.Run(pkgInfo.Type, func(t *testing.T) {
			t.Parallel()
			got, _ := migrate.Config(pkgInfo, nil)
			if got.Checksum != nil {
				t.Errorf("the definition still says where the checksum file is: %+v", got.Checksum)
			}
			for _, vo := range got.VersionOverrides {
				if vo.Checksum != nil {
					t.Errorf("an override still says where the checksum file is: %+v", vo.Checksum)
				}
			}
		})
	}
}
