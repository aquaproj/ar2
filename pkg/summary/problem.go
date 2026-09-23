package summary

import (
	"fmt"
	"os"
	"strings"
)

// Problem is a version a run didn't publish, and why.
type Problem struct {
	Package string
	Version string
	Reason  string
}

// Problems writes what the run couldn't publish.
//
// A version whose signature can't be verified isn't published, and the next run tries
// it again, so a release that has genuinely stopped being signed produces nothing but
// a line in a log nobody reads. Here it is where the run is read.
//
// It is written whatever the summary already holds. The budget the generated files
// spend is under what GitHub accepts, and this is the part worth keeping when they
// have used the rest.
func (w *Writer) Problems(problems []*Problem) error {
	if w.path == "" || len(problems) == 0 {
		return nil
	}
	var body strings.Builder
	body.WriteString("## Not published\n\n")
	body.WriteString("These versions were left out. The next run tries them again.\n\n")
	body.WriteString("| package | version | why |\n| --- | --- | --- |\n")
	for _, p := range problems {
		fmt.Fprintf(&body, "| %s | %s | %s |\n", p.Package, p.Version, cell(p.Reason))
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

// cell makes a reason fit in a table: a newline would end the row and a pipe would
// start a column.
func cell(reason string) string {
	reason = strings.ReplaceAll(reason, "\n", " ")
	return strings.ReplaceAll(reason, "|", `\|`)
}
