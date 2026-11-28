package g2_test

import (
	"slices"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v4"
)

// template-1 is the fields aqua's definition had when it was introduced. Updating aqua
// stops here at every field it adds or removes, so that someone decides whether the field
// can join template-1 (dropping it changes nothing an older aqua resolves) or needs
// template-2.
func TestTemplateFields(t *testing.T) {
	t.Parallel()
	known := append(g2.TemplateFields(), g2.TemplateExcludedFields()...)
	slices.Sort(known)
	if diff := cmp.Diff(known, g2.DefinitionFields()); diff != "" {
		t.Errorf("aqua's definition differs from what template-1 decided (-decided +aqua). For each field aqua added, decide whether an older aqua dropping it changes nothing (add it to pkg/g2/template_fields.txt) or changes what is resolved (add it to pkg/g2/template_excluded_fields.txt, for template-2):\n%s", diff)
	}
	for _, field := range g2.TemplateExcludedFields() {
		if slices.Contains(g2.TemplateFields(), field) {
			t.Errorf("%s is both allowed and excluded", field)
		}
	}
}

func templateDefinition(t *testing.T) *aquag2.Config {
	t.Helper()
	cfg := &aquag2.Config{}
	dec := yaml.NewDecoder(strings.NewReader(validTemplate))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

const validTemplate = `
name: example/cli
type: http
url: https://example.com/{{.Version}}/cli_{{.OS}}_{{.Arch}}.tar.gz
replacements:
  darwin: macos
files:
  - name: cli
    src: cli_{{.OS}}/cli
template:
  reason: the vendor publishes no list of versions
  test_versions:
    - v2.0.0
version_overrides:
  - version_constraint: semver(">= 2.0.0")
    checksum:
      type: http
      url: https://example.com/{{.Version}}/checksums.txt
      algorithm: sha256
    overrides:
      - goos: windows
        format: zip
  - version_constraint: "true"
    url: https://example.com/old/{{.Version}}
`

func TestValidateTemplate(t *testing.T) {
	t.Parallel()
	if err := g2.ValidateTemplate(templateDefinition(t)); err != nil {
		t.Fatalf("a valid template is refused: %v", err)
	}
	for name, edit := range map[string]func(*aquag2.Config){
		"no reason":         func(c *aquag2.Config) { c.Template.Reason = "" },
		"no test":           func(c *aquag2.Config) { c.Template.TestVersions = nil },
		"both":              func(c *aquag2.Config) { c.Template.SkipTest = "too large" },
		"top constraint":    func(c *aquag2.Config) { c.VersionConstraints = "false" },
		"all_assets_filter": func(c *aquag2.Config) { c.AllAssetsFilter = "true" },
		"asset_rules":       func(c *aquag2.Config) { c.AssetRules = []*aquag2.AssetRule{{Envs: []string{"linux/amd64"}}} },
		"prerelease":        func(c *aquag2.Config) { c.Prerelease = true },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := templateDefinition(t)
			edit(cfg)
			if err := g2.ValidateTemplate(cfg); err == nil {
				t.Error("must be refused")
			}
		})
	}
	skip := templateDefinition(t)
	skip.Template.TestVersions = nil
	skip.Template.SkipTest = "the asset is too large to download in time"
	if err := g2.ValidateTemplate(skip); err != nil {
		t.Errorf("skip_test alone is allowed: %v", err)
	}
}

// What is written is aqua's definition of the package, without what only generating reads.
func TestMarshalTemplate(t *testing.T) {
	t.Parallel()
	cfg := templateDefinition(t)
	cfg.RepoID = 1234
	b, err := g2.MarshalTemplate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"template"`, `"repo_id"`, `"RepoID"`, `"Template"`} {
		if strings.Contains(s, key) {
			t.Errorf("the template has %s", key)
		}
	}
	tmpl, err := aquag2.ParseTemplate(b)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(aquag2.NewTemplate(cfg), tmpl.PackageInfo); diff != "" {
		t.Errorf("the template doesn't round-trip (-want +got):\n%s", diff)
	}
}
