package validatetemplates

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v4"
)

const templateDefinition = `name: example/cli
type: http
url: https://example.com/{{.Version}}/cli.tar.gz
template:
  reason: no list of versions
  test_versions:
    - v1.0.0
`

func rendered(t *testing.T, def string) string {
	t.Helper()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(def), cfg); err != nil {
		t.Fatal(err)
	}
	b, err := g2.MarshalTemplate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// layout writes a package directory and returns the template's path.
func layout(t *testing.T, def, tmpl string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pkgs", "01", "1795000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if def != "" {
		if err := os.WriteFile(filepath.Join(dir, g2.ConfigFileName), []byte(def), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, g2.TemplateFileName)
	if tmpl != "" {
		if err := os.WriteFile(path, []byte(tmpl), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestValidate(t *testing.T) {
	t.Parallel()
	ar2yaml := filepath.Join(t.TempDir(), "ar2.yaml")
	if err := os.WriteFile(ar2yaml, []byte("ignored_packages:\n  - name: example/ignored\n    reason: malware\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notTemplate := "name: example/cli\ntype: github_release\nrepo_owner: example\nrepo_name: cli\n"
	ignored := strings.Replace(templateDefinition, "example/cli", "example/ignored", 1)
	for name, tc := range map[string]struct {
		def, tmpl string
		ok        bool
	}{
		"rendered":              {templateDefinition, rendered(t, templateDefinition), true},
		"edited by hand":        {templateDefinition, rendered(t, templateDefinition) + " ", false},
		"not a template":        {notTemplate, `{"name":"example/cli"}`, false},
		"ignored":               {ignored, rendered(t, ignored), false},
		"no definition":         {"", `{"name":"example/cli"}`, false},
		"removed, still one":    {templateDefinition, "", false},
		"removed, ignored":      {ignored, "", true},
		"removed, not template": {notTemplate, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := Validate(io.Discard, ar2yaml, []string{layout(t, tc.def, tc.tmpl)})
			if tc.ok && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("must be refused")
			}
		})
	}
}
