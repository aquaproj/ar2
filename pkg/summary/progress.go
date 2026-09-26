package summary

import (
	"fmt"
	"strings"

	"github.com/aquaproj/ar2/pkg/state"
)

// Progress writes how far the registry has got.
//
// A run is read for what it generated, which says nothing about how much is left. The
// backfill is the whole of what the registry is doing for now, and this is the only place
// the numbers exist: the repository can be counted for branches, and the turns each
// package has had are in the state and nowhere else.
func (w *Writer) Progress(p *state.Progress) error {
	if w.path == "" || p == nil {
		return nil
	}
	var body strings.Builder
	body.WriteString("## Progress\n\n")
	body.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(&body, "| packages in the order | %d |\n", p.Packages)
	fmt.Fprintf(&body, "| holding every version last swept | %d |\n", p.CaughtUp)
	fmt.Fprintf(&body, "| no run has reached | %d |\n", p.Untouched)
	fmt.Fprintf(&body, "| history never walked | %d |\n", p.Unwalked)
	fmt.Fprintf(&body, "| turns the order is on | %d |\n", p.Lap)
	fmt.Fprintf(&body, "| waiting for that turn | %d |\n", p.Waiting)
	if p.Renamed > 0 {
		fmt.Fprintf(&body, "| names moved to another | %d |\n", p.Renamed)
	}
	body.WriteString("\n")
	return w.append(body.String())
}
