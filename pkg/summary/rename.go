package summary

import (
	"fmt"
	"os"
	"strings"
)

// Rename is a repository that isn't where the registry says it is.
type Rename struct {
	From string
	To   string
}

// Renames writes the repositories the sweep found have been renamed.
//
// The sweep asks every repository what it is called, and GitHub answers a query made
// with an old name, so this costs nothing to find out. Nothing is done about it yet:
// the branch holding a package's generated versions is named after the name the
// registry has, and moving it is a run of its own.
//
// Written where the run is read rather than left in the log, because it is the registry
// being out of date about what a package is. That stays true until somebody acts on it,
// and a line in a log nobody reads is how it would stay that way.
func (w *Writer) Renames(renames []*Rename) error {
	if w.path == "" || len(renames) == 0 {
		return nil
	}
	var body strings.Builder
	body.WriteString("## Renamed\n\n")
	body.WriteString("These repositories answer to another name now. The registry still holds " +
		"them under the old one, and the branch with their generated versions is named after it.\n\n")
	body.WriteString("| the registry says | GitHub says |\n| --- | --- |\n")
	for _, r := range renames {
		fmt.Fprintf(&body, "| %s | %s |\n", cell(r.From), cell(r.To))
	}
	body.WriteString("\n")

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("open the job summary: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(body.String()); err != nil {
		return fmt.Errorf("write the job summary: %w", err)
	}
	return nil
}
