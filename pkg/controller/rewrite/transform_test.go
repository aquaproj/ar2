package rewrite_test

import (
	"os"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/controller/rewrite"
	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// kubernetes/kubernetes/apiextensions-apiserver v1.34.11, as the registry published it: on
// one line, with cosign's files and identity on its command line.
func TestFile(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(read(t, "testdata/registry.yaml")), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.RepoID = 20580498
	got, err := rewrite.File(read(t, "testdata/cosign.json"), cfg, "v1.34.11")
	if err != nil {
		t.Fatal(err)
	}
	if update := os.Getenv("UPDATE_GOLDEN"); update != "" {
		if err := os.WriteFile("testdata/cosign.golden.json", []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if diff := cmp.Diff(read(t, "testdata/cosign.golden.json"), got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	// Rendering again what was rendered changes nothing, which is what lets the check
	// derive it again.
	again, err := rewrite.File(got, cfg, "v1.34.11")
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Error("rendering the rewritten file again changed it")
	}
}

// A field the type doesn't know is refused rather than dropped.
func TestFile_unknownField(t *testing.T) {
	t.Parallel()
	if _, err := rewrite.File(`{"assets":[],"surprise":1}`, &aquag2.Config{}, "v1.0.0"); err == nil {
		t.Error("an unknown field was accepted")
	}
}

func TestDefinition(t *testing.T) {
	t.Parallel()
	in := "# kept\nname: cli/cli\nrepo_owner: cli\nrepo_name: cli\nfiles:\n  - name: gh # kept too\n"
	got, err := rewrite.Definition(in, 212613049)
	if err != nil {
		t.Fatal(err)
	}
	want := "# kept\nname: cli/cli\nrepo_owner: cli\nrepo_name: cli\nrepo_id: 212613049\nfiles:\n  - name: gh # kept too\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if again, err := rewrite.Definition(got, 212613049); err != nil || again != got {
		t.Errorf("a definition with the id already: %q, %v", again, err)
	}
	if _, err := rewrite.Definition(got, 1); err == nil {
		t.Error("another id was accepted")
	}
	if _, err := rewrite.Definition("name: x\n  repo_name: nested\n", 1); err == nil || !strings.Contains(err.Error(), "repo_name") {
		t.Errorf("no top-level repo_name: %v", err)
	}
}
