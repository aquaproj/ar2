package summary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szksh-lab-2/ar2/pkg/summary"
)

// TestWriter checks that the indented form of what a run generated reaches the job
// summary. registry.json itself is stored on one line, so this is where it can be
// read.
func TestWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)

	w := summary.New()
	if err := w.Add("cli/cli", map[string]any{
		"v2.2.0": map[string]string{"asset": "gh_2.2.0.tar.gz"},
		"v2.1.0": map[string]string{"asset": "gh_2.1.0.tar.gz"},
	}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"## cli/cli", "### v2.1.0", "### v2.2.0", "```json", `"asset": "gh_2.1.0.tar.gz"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary doesn't contain %q\ngot:\n%s", want, got)
		}
	}
	// Sorted, so that the same run reads the same way twice.
	if strings.Index(got, "### v2.1.0") > strings.Index(got, "### v2.2.0") {
		t.Error("versions should be sorted")
	}
}

// TestWriter_noEnv checks that ar2 running outside GitHub Actions writes nothing.
func TestWriter_noEnv(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	if err := summary.New().Add("cli/cli", map[string]any{"v1": 1}); err != nil {
		t.Fatal(err)
	}
}
