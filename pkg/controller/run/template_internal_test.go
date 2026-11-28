package run

import (
	"context"
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/state"
)

// definitionRegistry holds one definition for every package asked about.
type definitionRegistry struct {
	fakeRegistry
	cfg *aquag2.Config
}

func (r *definitionRegistry) Config(_ context.Context, _ string) (*aquag2.Config, error) {
	return r.cfg, nil
}

func templateConfig() *aquag2.Config {
	return &aquag2.Config{
		PackageInfo: &aquaregistry.PackageInfo{Name: "example/cli", Type: "http", URL: "https://example.com/{{.Version}}"},
		Template:    &aquag2.Template{Reason: "no list of versions", SkipTest: "too large"},
	}
}

func generatedConfig() *aquag2.Config {
	return &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		Name: "example/cli", Type: "github_release", RepoOwner: "example", RepoName: "cli",
	}}
}

// A package without a repository is marked when its definition declares a template, and the
// mark comes off when it no longer does. Without a definition it is neither.
func TestController_noRepository(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	for name, tc := range map[string]struct {
		cfg    *aquag2.Config
		before bool
		want   bool
	}{
		"template":           {templateConfig(), false, true},
		"no longer template": {generatedConfig(), true, false},
		"no definition":      {nil, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &Controller{g2: &definitionRegistry{cfg: tc.cfg}}
			candidate := &Candidate{Name: "example/cli", Package: &state.Package{Template: tc.before}}
			if err := c.noRepository(t.Context(), logger, candidate); err != nil {
				t.Fatal(err)
			}
			if candidate.Package.Template != tc.want {
				t.Errorf("template is %v, want %v", candidate.Package.Template, tc.want)
			}
		})
	}
}

// A held definition that declares a template generates nothing and marks the package; one
// that doesn't is generated from, and takes the mark off.
func TestController_definitionToGenerate(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	c := &Controller{g2: &definitionRegistry{cfg: templateConfig()}}
	candidate := &Candidate{Name: "example/cli", Package: &state.Package{RepoOwner: "example", RepoName: "cli"}}
	def, err := c.definitionToGenerate(t.Context(), logger, &Input{}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if def != nil || !candidate.Package.Template {
		t.Errorf("a template generates nothing and is marked: def %v, template %v", def, candidate.Package.Template)
	}

	c = &Controller{g2: &definitionRegistry{cfg: generatedConfig()}}
	candidate.Package.Template = true
	def, err = c.definitionToGenerate(t.Context(), logger, &Input{}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if def == nil || candidate.Package.Template {
		t.Errorf("a generated package is generated from and unmarked: def %v, template %v", def, candidate.Package.Template)
	}
}
