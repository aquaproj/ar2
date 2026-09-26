package run

import (
	"log/slog"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/google/go-cmp/cmp"
)

func TestPackageConfig(t *testing.T) {
	t.Parallel()
	converted := &aquag2.Config{
		PackageInfo:     &aquaregistry.PackageInfo{Type: "github_release", RepoOwner: "ollama", RepoName: "ollama"},
		AllAssetsFilter: `not (Asset matches "rocm")`,
	}
	data := []struct {
		name string
		def  *definition
		file bool
		// review says the pull request must not merge itself.
		review bool
	}{
		{
			// Every package but the first time it is worked on.
			name: "the branch already has one",
			def:  &definition{config: converted, fromBranch: true},
		},
		{
			// aqua-registry doesn't have the package; what was generated came from
			// the release alone.
			name: "there was nothing to convert",
			def:  &definition{},
		},
		{
			name: "converted",
			def:  &definition{config: converted},
			file: true,
		},
		{
			name:   "converted, with a constraint that couldn't be read",
			def:    &definition{config: converted, unconverted: []string{`Version in ["v1.0.0"]`}},
			file:   true,
			review: true,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			c := &Controller{}
			got, err := c.packageConfig(slog.New(slog.DiscardHandler), d.def, "ollama/ollama", nil)
			if err != nil {
				t.Fatalf("packageConfig(): %v", err)
			}
			if !d.file {
				if got != nil {
					t.Fatalf("packageConfig() returned a file to commit: %v", got.file)
				}
				return
			}
			if got == nil {
				t.Fatal("packageConfig() returned nothing to commit")
			}
			if got.needsReview != d.review {
				t.Fatalf("needsReview = %v, wanted %v", got.needsReview, d.review)
			}
			// The filters are what the definition is kept for: a definition committed
			// without them wouldn't produce the files committed beside it.
			if !strings.Contains(got.file.Content, "all_assets_filter") {
				t.Fatalf("the definition doesn't carry the filter:\n%s", got.file.Content)
			}
		})
	}
}

// A package renamed before it was ever generated has no branch to carry the alias, so the
// definition it arrives with has to say the old name itself. Otherwise a configuration
// asking for the name aqua-registry still has resolves to nothing.
func TestAddFormerNames(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{}}
	s := &state.State{Renamed: map[string]string{
		"mgechev/revive": "revive-lint/revive",
		"docker/cagent":  "docker/docker-agent",
		"block/goose":    "revive-lint/revive",
	}}
	addFormerNames(discardLogger(), cfg, "revive-lint/revive", s)

	names := make([]string, 0, len(cfg.Aliases))
	for _, alias := range cfg.Aliases {
		names = append(names, alias.Name)
	}
	if diff := cmp.Diff([]string{"block/goose", "mgechev/revive"}, names); diff != "" {
		t.Errorf("the aliases are wrong (-want +got):\n%s", diff)
	}
}

// A name the definition already lists is not listed twice.
func TestAddFormerNames_alreadyThere(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		Aliases: []*aquaregistry.Alias{{Name: "mgechev/revive"}},
	}}
	s := &state.State{Renamed: map[string]string{"mgechev/revive": "revive-lint/revive"}}
	addFormerNames(discardLogger(), cfg, "revive-lint/revive", s)
	if len(cfg.Aliases) != 1 {
		t.Errorf("the aliases are %+v, want the one that was there", cfg.Aliases)
	}
}
