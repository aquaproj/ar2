// Package summary writes to the GitHub Actions job summary.
//
// registry.json is stored on one line: nothing reads it by eye, and the indentation
// is 30% of the bytes every aqua user downloads while costing nothing in the
// repository, where git compresses it away. The indented form is written here
// instead, so that what a run produced can still be read when someone needs to.
package summary

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// envVar is where GitHub Actions tells a step to append its summary.
const envVar = "GITHUB_STEP_SUMMARY"

// filePerm is the mode the summary file is created with when it doesn't exist.
const filePerm = 0o600

// maxBytes keeps the summary under the 1 MiB GitHub accepts. A run generating
// hundreds of versions would otherwise have its summary rejected as a whole, losing
// the part that fits along with the part that doesn't.
const maxBytes = 900 * 1024

// Writer appends to the job summary. A Writer with no path does nothing, which is
// what happens outside GitHub Actions.
type Writer struct {
	path    string
	written int
}

// New creates a Writer for the current job.
func New() *Writer {
	return &Writer{path: os.Getenv(envVar)}
}

// Add writes one package's generated files to the summary, indented.
func (w *Writer) Add(pkgName string, versions map[string]any) error {
	if w.path == "" || w.written >= maxBytes {
		return nil
	}
	var body strings.Builder
	body.WriteString("## " + pkgName + "\n\n")
	// Sorted so that a run's summary reads the same way twice.
	for _, version := range slices.Sorted(maps.Keys(versions)) {
		b, err := json.MarshalIndent(versions[version], "", "  ")
		if err != nil {
			return fmt.Errorf("marshal registry.json for the summary: %w", err)
		}
		fmt.Fprintf(&body, "### %s\n\n```json\n%s\n```\n\n", version, b)
	}
	return w.append(body.String())
}

func (w *Writer) append(body string) error {
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("open the job summary: %w", err)
	}
	defer f.Close()
	if w.written+len(body) > maxBytes {
		body = "The summary is truncated: the run generated more than fits in it.\n"
		w.written = maxBytes
	} else {
		w.written += len(body)
	}
	if _, err := f.WriteString(body); err != nil {
		return fmt.Errorf("write the job summary: %w", err)
	}
	return nil
}
