package test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/aquaproj/aqua/v2/pkg/config"
	"github.com/aquaproj/aqua/v2/pkg/config/aqua"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/verify"
	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v4"
)

// recordingVerifier answers every download as an archive holding what the entry names, and
// remembers which URLs were asked for.
type recordingVerifier struct {
	urls   []string
	result *verify.Result
}

func (v *recordingVerifier) Verify(_ context.Context, _ *slog.Logger, _, _ string, asset *generate.Asset) (*verify.Result, error) {
	v.urls = append(v.urls, asset.URL)
	if v.result != nil {
		return v.result, nil
	}
	return &verify.Result{Checksum: "aaa"}, nil
}

type recordingChecksums struct {
	urls []string
	err  error
}

func (c *recordingChecksums) CheckListedChecksum(_ context.Context, _ *slog.Logger, _ string, _ *generate.Asset, checksumURL, _ string, _ *aquaregistry.Checksum) error {
	c.urls = append(c.urls, checksumURL)
	return c.err
}

const templateDefinition = `name: example/cli
type: http
url: https://example.com/{{.Version}}/cli_{{.OS}}_{{.Arch}}.tar.gz
supported_envs:
  - linux/amd64
  - darwin/arm64
files:
  - name: cli
template:
  reason: no list of versions
  test_versions:
    - v2.0.0
    - v1.0.0
version_overrides:
  - version_constraint: semver(">= 2.0.0")
    checksum:
      type: http
      url: https://example.com/{{.Version}}/checksums.txt
      algorithm: sha256
  - version_constraint: "true"
    url: https://example.com/old/{{.Version}}/cli-{{.OS}}-{{.Arch}}.tar.gz
`

func templateConfig(t *testing.T, s string) *aquag2.Config {
	t.Helper()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(s), cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Each test version is expanded the way aqua does -- version_overrides newest first -- and
// the checksum file is consulted only where the definition names one.
func TestController_RunTemplate(t *testing.T) {
	t.Parallel()
	v := &recordingVerifier{}
	checksums := &recordingChecksums{}
	c := New(v, Environment{OS: "linux", Arch: "amd64"})
	if err := c.RunTemplate(t.Context(), slog.New(slog.DiscardHandler), templateConfig(t, templateDefinition), checksums); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://example.com/v2.0.0/cli_linux_amd64.tar.gz",
		"https://example.com/old/v1.0.0/cli-linux-amd64.tar.gz",
	}
	if diff := cmp.Diff(want, v.urls); diff != "" {
		t.Errorf("downloaded the wrong assets (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"https://example.com/v2.0.0/checksums.txt"}, checksums.urls); diff != "" {
		t.Errorf("read the wrong checksum files (-want +got):\n%s", diff)
	}
}

// A file the definition names but the archive doesn't hold, or a checksum file that
// disagrees, fails the test.
func TestController_RunTemplate_fails(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	cfg := templateConfig(t, templateDefinition)
	missing := New(&recordingVerifier{result: &verify.Result{Unresolved: []string{"cli"}}}, Environment{OS: "linux", Arch: "amd64"})
	if err := missing.RunTemplate(t.Context(), logger, cfg, &recordingChecksums{}); err == nil {
		t.Error("a missing file must fail")
	}
	tampered := New(&recordingVerifier{}, Environment{OS: "linux", Arch: "amd64"})
	if err := tampered.RunTemplate(t.Context(), logger, cfg, &recordingChecksums{err: errors.New("tampered")}); err == nil {
		t.Error("a disagreeing checksum file must fail")
	}
}

// A template that says why it can't be installed in CI is reported and passes.
func TestController_RunTemplate_skip(t *testing.T) {
	t.Parallel()
	cfg := templateConfig(t, strings.Replace(templateDefinition, "  test_versions:\n    - v2.0.0\n    - v1.0.0\n", "  skip_test: too large\n", 1))
	v := &recordingVerifier{}
	if err := New(v, Environment{}).RunTemplate(t.Context(), slog.New(slog.DiscardHandler), cfg, nil); err != nil {
		t.Fatal(err)
	}
	if len(v.urls) != 0 {
		t.Errorf("downloaded %v", v.urls)
	}
}

func TestTemplateEnvironments(t *testing.T) {
	t.Parallel()
	envs, err := TemplateEnvironments(slog.New(slog.DiscardHandler), templateConfig(t, templateDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(envs, []string{"darwin/arm64", "linux/amd64"}) {
		t.Errorf("got %v", envs)
	}
}

// A checksum file of a GitHub release is downloaded from the release the asset is in.
func TestChecksumFileURL_githubRelease(t *testing.T) {
	t.Parallel()
	pkg := &config.Package{
		Package: &aqua.Package{Name: "example/cli", Version: "v1.0.0"},
		PackageInfo: &aquaregistry.PackageInfo{
			Type: "github_release", RepoOwner: "example", RepoName: "cli",
			Asset:    "cli_{{.OS}}_{{.Arch}}.tar.gz",
			Checksum: &aquaregistry.Checksum{Type: "github_release", Asset: "cli_{{.Version}}_checksums.txt", Algorithm: "sha256"},
		},
	}
	got, err := checksumFileURL(pkg, &runtime.Runtime{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://github.com/example/cli/releases/download/v1.0.0/cli_v1.0.0_checksums.txt"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
