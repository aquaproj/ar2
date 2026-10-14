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
	cfg, _ := migrate.Config("cli/cli", &aquaregistry.PackageInfo{
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
	cfg, _ := migrate.Config("cli/cli", &aquaregistry.PackageInfo{
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
// asset it always had; nothing reading this registry is that old. A variant's asset whose
// name says musl says nothing either: the generator reads the libc off the name.
func TestConfig_conditionOnlyOverride(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("cli/cli", pnpm("pnpm-{{.OS}}-{{.Arch}}-musl.{{.Format}}"), nil)
	if len(cfg.VersionOverrides) == 0 {
		t.Fatal("got no version_overrides")
	}
	if overrides := cfg.VersionOverrides[0].Overrides; len(overrides) != 0 {
		t.Fatalf("got %d overrides, want none:\n%+v", len(overrides), overrides)
	}
}

// A variant's asset whose name doesn't say its libc is the one thing the asset list can't be
// read for, so the override naming it stays, with its format: the first override that
// matches is the only one applied, so what is inferred for the OS elsewhere doesn't reach the
// asset named here.
func TestConfig_unreadableVariant(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("cli/cli", pnpm("pnpm-alpine-{{.Arch}}.{{.Format}}"), nil)
	if len(cfg.VersionOverrides) == 0 {
		t.Fatal("got no version_overrides")
	}
	overrides := cfg.VersionOverrides[0].Overrides
	if len(overrides) != 1 {
		t.Fatalf("got %d overrides, want the one that says something:\n%+v", len(overrides), overrides)
	}
	if got := overrides[0].Asset; got != "pnpm-alpine-{{.Arch}}.{{.Format}}" {
		t.Errorf("the asset is %q, want the musl one", got)
	}
	if got := overrides[0].Format; got != "tar.gz" {
		t.Errorf("the format is %q, want tar.gz", got)
	}
	if len(overrides[0].Variants) != 1 || overrides[0].Variants[0].Value != "musl" {
		t.Errorf("the override matches %+v, want the musl variant", overrides[0].Variants)
	}
}

// pnpm is a definition whose musl build is the given asset.
func pnpm(musl string) *aquaregistry.PackageInfo {
	return &aquaregistry.PackageInfo{
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
						Asset:    musl,
						Format:   "tar.gz",
						Variants: []*aquaregistry.Variant{{Key: "libc", Value: "musl"}},
					},
				},
			},
		},
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
			got, _ := migrate.Config("cli/cli", pkgInfo, nil)
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

// The definition says which package it is for, always. aqua-registry leaves the name out
// when it is the repository's, because there the definition sits in a file named after the
// package; here it sits on a branch, and the only thing that says which package a branch
// holds is the definition on it.
func TestConfig_alwaysNamesThePackage(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		pkgName string
		base    *aquaregistry.PackageInfo
	}{
		{
			// v1 leaves it out: the name is the repository's.
			pkgName: "cli/cli",
			base:    &aquaregistry.PackageInfo{Type: "github_release", RepoOwner: "cli", RepoName: "cli"},
		},
		{
			// One repository publishing several commands names each of them.
			pkgName: "kubernetes/kubernetes/kubectl",
			base: &aquaregistry.PackageInfo{
				Name: "kubernetes/kubernetes/kubectl", Type: "github_release",
				RepoOwner: "kubernetes", RepoName: "kubernetes",
			},
		},
		{
			// Every other type is carried over as it is, and this still has to hold.
			pkgName: "apache/tomcat",
			base:    &aquaregistry.PackageInfo{Type: "http", URL: "https://example.com/{{.Version}}"},
		},
	} {
		t.Run(d.pkgName, func(t *testing.T) {
			t.Parallel()
			cfg, _ := migrate.Config(d.pkgName, d.base, nil)
			if cfg.Name != d.pkgName {
				t.Fatalf("the definition says %q", cfg.Name)
			}
		})
	}
}

// supported_envs is kept: a release can publish an asset for an environment that doesn't
// hold the package, which its asset names don't say. kcp-dev/kcp's Windows archives hold
// only its kubectl plugins.
func TestConfig_supportedEnvs(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("kcp-dev/kcp", &aquaregistry.PackageInfo{
		Type:          "github_release",
		RepoOwner:     "kcp-dev",
		RepoName:      "kcp",
		Asset:         "kcp_{{trimV .Version}}_{{.OS}}_{{.Arch}}.{{.Format}}",
		Format:        "tar.gz",
		SupportedEnvs: []string{"darwin", "linux"},
	}, nil)
	if diff := cmp.Diff(aquaregistry.SupportedEnvs{"darwin", "linux"}, cfg.SupportedEnvs); diff != "" {
		t.Errorf("supported_envs (-want +got):\n%s", diff)
	}

	cfg, _ = migrate.Config("kcp-dev/kcp", &aquaregistry.PackageInfo{
		Type:               "github_release",
		RepoOwner:          "kcp-dev",
		RepoName:           "kcp",
		VersionConstraints: "false",
		VersionOverrides: []*aquaregistry.VersionOverride{
			{
				VersionConstraints: "true",
				Asset:              "kcp_{{trimV .Version}}_{{.OS}}_{{.Arch}}.{{.Format}}",
				Format:             "tar.gz",
				SupportedEnvs:      []string{"linux"},
			},
		},
	}, nil)
	if len(cfg.VersionOverrides) == 0 {
		t.Fatal("got no version_overrides")
	}
	if diff := cmp.Diff(aquaregistry.SupportedEnvs{"linux"}, cfg.VersionOverrides[0].SupportedEnvs); diff != "" {
		t.Errorf("the version override's supported_envs (-want +got):\n%s", diff)
	}
}

// An override naming the executables keeps its format, because a release can publish the
// build in two: tamasfe/taplo's Windows build is a .gz and a .zip, and only the .zip holds
// taplo.exe. Without them the format is read off the asset names.
func TestConfig_overrideFormatWithFiles(t *testing.T) {
	t.Parallel()
	cfg, _ := migrate.Config("tamasfe/taplo", &aquaregistry.PackageInfo{
		Type:      "github_release",
		RepoOwner: "tamasfe",
		RepoName:  "taplo",
		Asset:     "taplo-{{.OS}}-{{.Arch}}.{{.Format}}",
		Format:    "gz",
		Files:     []*aquaregistry.File{{Name: "taplo", Src: "taplo-{{.OS}}-{{.Arch}}"}},
		Overrides: []*aquaregistry.Override{
			{GOOS: "windows", Format: "zip", Files: []*aquaregistry.File{{Name: "taplo", Src: "taplo.exe"}}},
		},
	}, nil)

	if cfg.Format != "" {
		t.Errorf("the package's format is %q, want it read off the asset names", cfg.Format)
	}
	if len(cfg.Overrides) != 1 {
		t.Fatalf("got %d overrides, want 1", len(cfg.Overrides))
	}
	if got := cfg.Overrides[0].Format; got != "zip" {
		t.Errorf("the Windows override's format is %q, want zip", got)
	}
}
