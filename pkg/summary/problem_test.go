package summary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aquaproj/ar2/pkg/summary"
)

func TestWriter_Problems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	w := summary.New()

	if err := w.Problems([]*summary.Problem{
		{
			Package: "kubernetes/kubernetes/kube-log-runner",
			Version: "v1.36.4",
			// A reason arrives as the error was written: a pipe would start a
			// column and a newline would end the row.
			Reason: "cosign: verify with Cosign: Error: none of the expected identities matched\nexit status 1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"## Not published", "kube-log-runner", "v1.36.4", "none of the expected identities matched"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary doesn't mention %q:\n%s", want, got)
		}
	}
	// The header, the separator and one row: the reason didn't bring a row of its
	// own, whatever it held.
	rows := 0
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "|") {
			rows++
		}
	}
	if rows != 3 {
		t.Errorf("the table has %d lines, want 3:\n%s", rows, got)
	}
}

// Nothing to say is nothing written, so a run that published everything gets no
// section at all.
func TestWriter_Problems_none(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	if err := summary.New().Problems(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the summary file was created: %v", err)
	}
}
