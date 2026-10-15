package run

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
)

// cmctl's definition: the versions after v1.14.4 moved to another repository, and the
// definition says so rather than naming an asset.
const cmctl = `
type: github_release
repo_owner: cert-manager
repo_name: cert-manager
version_overrides:
  - version_constraint: semver("> 1.14.4")
    error_message: Please use the package cert-manager/cmctl instead.
  - version_constraint: semver("> 1.0.0")
    asset: cmctl-{{.OS}}-{{.Arch}}.tar.gz
  - version_constraint: "true"
    no_asset: true
`

// A version the definition says has nothing to install, and one whose release turned out
// to hold no asset, are settled the way a version the registry holds is.
func TestSettled(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(cmctl), cfg); err != nil {
		t.Fatal(err)
	}
	pkg := &state.Package{EmptyReleases: []string{"v1.14.3"}}
	versions := []string{"v1.21.2", "v1.14.4", "v1.14.3", "v1.14.2", "v0.9.0"}
	got := settled(slog.New(slog.DiscardHandler), &definition{config: cfg}, pkg, versions,
		map[string]struct{}{"v1.14.2": {}})
	want := map[string]struct{}{
		"v1.21.2": {}, // error_message
		"v1.14.3": {}, // its release has no asset
		"v1.14.2": {}, // the registry holds it
		"v0.9.0":  {}, // no_asset
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("settled (-want +got):\n%s", diff)
	}
}

// Without a definition there is nothing to read, and every version is generated to find out.
func TestSettled_noDefinition(t *testing.T) {
	t.Parallel()
	got := settled(slog.New(slog.DiscardHandler), &definition{}, &state.Package{}, []string{"v1.0.0"}, nil)
	if len(got) != 0 {
		t.Errorf("got %v, want nothing settled", got)
	}
}

// A release with no assets is passed over from then on only once it has had a day to get
// some: one read while its assets are still uploading is tried again.
func TestEmptyRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "published a while ago",
			err:  &generate.EmptyReleaseError{PublishedAt: now.Add(-48 * time.Hour)},
			want: true,
		},
		{
			name: "just published",
			err:  &generate.EmptyReleaseError{PublishedAt: now.Add(-time.Hour)},
		},
		{
			name: "never published",
			err:  &generate.EmptyReleaseError{},
		},
		{
			name: "another error",
			err:  errors.New("the release has no asset for any environment"),
		},
		{
			name: "no error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pkg := &state.Package{}
			if got := emptyRelease(pkg, "v13.0.0", tt.err, now); got != tt.want {
				t.Fatalf("got %t, want %t", got, tt.want)
			}
			var want []string
			if tt.want {
				want = []string{"v13.0.0"}
			}
			if diff := cmp.Diff(want, pkg.EmptyReleases); diff != "" {
				t.Errorf("recorded (-want +got):\n%s", diff)
			}
		})
	}
}

// A version that tied is passed over while the definition is the one it tied under, and
// tried again once the definition changes: a filter added to it is what settles the tie.
func TestSettled_tie(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(cmctl), cfg); err != nil {
		t.Fatal(err)
	}
	def := &definition{config: cfg}
	pkg := &state.Package{}
	tied(pkg, def, "v1.14.4")
	logger := slog.New(slog.DiscardHandler)
	if _, ok := settled(logger, def, pkg, []string{"v1.14.4"}, nil)["v1.14.4"]; !ok {
		t.Error("a version that tied under the same definition is tried again")
	}
	cfg.AllAssetsFilter = `not (Asset contains "bazel")`
	if _, ok := settled(logger, def, pkg, []string{"v1.14.4"}, nil)["v1.14.4"]; ok {
		t.Error("a version that tied under another definition isn't tried again")
	}
}

// A tie in every environment is recorded and said as the environments it leaves out; any
// other error isn't a tie.
func TestController_tie(t *testing.T) {
	t.Parallel()
	c := &Controller{}
	candidate := &Candidate{Name: "peripheryapp/periphery", Package: &state.Package{RepoOwner: "peripheryapp", RepoName: "periphery"}}
	err := fmt.Errorf("generate registry.json: %w", &generate.TieError{Left: []*generate.Left{
		{OS: "darwin", Arch: "arm64", Reason: "assets of an environment can't be told apart by their names: periphery-3.8.0.zip, periphery-bazel-3.8.0.zip"},
	}})
	v := c.tie(slog.New(slog.DiscardHandler), &definition{}, candidate, "3.8.0", err)
	if v == nil || len(v.Excluded) != 1 || v.Excluded[0].Asset.RepoName != "periphery" {
		t.Fatalf("got %+v, want darwin/arm64 left out", v)
	}
	if _, ok := candidate.Package.Ties["3.8.0"]; !ok {
		t.Error("the tie wasn't recorded")
	}
	if c.tie(slog.New(slog.DiscardHandler), &definition{}, candidate, "3.7.4", errors.New("something else")) != nil {
		t.Error("another error was taken for a tie")
	}
}
