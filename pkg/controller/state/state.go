// Package state prints what ar2's state says.
//
// The state decides which package a run works on next, and nothing else can be asked
// about it: the repository says what the registry holds, not whose turn it is. So "why
// hasn't this package been generated" has no answer anywhere else, and neither has "how
// far along is the backfill" beyond counting branches.
//
// It only reads, which makes it the one thing here that runs from a laptop: everything
// that writes has to be a workflow, because a commit made with a user access token isn't
// signed and the rulesets require signatures.
package state

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/aquaproj/ar2/pkg/state"
)

// fingerprintChars is how much of the versions fingerprint is worth printing.
//
// What it answers is whether the versions are the same as last time, so what a person
// needs from it is whether it changed between two runs. The rest is 52 more characters of
// hex in a report read by eye.
const fingerprintChars = 12

// Summary writes how far the registry has got.
func Summary(w io.Writer, s *state.State) error {
	p := s.Progress()
	var b strings.Builder
	fmt.Fprintf(&b, "packages in the order            %d\n", p.Packages)
	fmt.Fprintf(&b, "holding every version swept      %d\n", p.CaughtUp)
	fmt.Fprintf(&b, "no run has reached               %d\n", p.Untouched)
	fmt.Fprintf(&b, "history never walked             %d\n", p.Unwalked)
	fmt.Fprintf(&b, "turns the order is on            %d\n", p.Lap)
	fmt.Fprintf(&b, "waiting for that turn            %d\n", p.Waiting)
	fmt.Fprintf(&b, "names moved to another           %d\n", p.Renamed)
	if !s.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "written                          %s\n", s.UpdatedAt.Format(time.RFC3339))
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write the summary: %w", err)
	}
	return nil
}

// Packages writes what the state says about each named package.
//
// A name the order doesn't hold is said so rather than skipped: it is the answer to why
// nothing has been generated for it, and the state is where a package is added to the
// order at all.
func Packages(w io.Writer, s *state.State, names []string) error {
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString("\n")
		}
		writePackage(&b, s, name)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write the packages: %w", err)
	}
	return nil
}

func writePackage(b *strings.Builder, s *state.State, name string) {
	fmt.Fprintf(b, "%s\n", name)
	if to, ok := s.Renamed[name]; ok {
		// The registry stopped holding this name. Whatever else is said about it is
		// said about the name it moved to.
		fmt.Fprintf(b, "  moved to                       %s\n", to)
	}
	pkg, ok := s.Packages[name]
	if !ok || pkg == nil {
		b.WriteString("  not in the order: no run will reach it until it is added\n")
		return
	}
	if pkg.RepoOwner != "" {
		fmt.Fprintf(b, "  repository                     %s/%s\n", pkg.RepoOwner, pkg.RepoName)
	}
	fmt.Fprintf(b, "  stars                          %d\n", pkg.Stars)
	fmt.Fprintf(b, "  turns                          %d\n", pkg.Round)
	fmt.Fprintf(b, "  behind the order by            %d\n", pkg.Round-s.Progress().Lap)
	fmt.Fprintf(b, "  holds every version swept      %t\n", pkg.CaughtUp)
	if pkg.LastDeepCheck.IsZero() {
		b.WriteString("  history walked                 never\n")
	} else {
		fmt.Fprintf(b, "  history walked                 %s\n", pkg.LastDeepCheck.Format(time.RFC3339))
	}
	if pkg.Versions != "" {
		fmt.Fprintf(b, "  versions last swept            %s\n", pkg.Versions[:min(fingerprintChars, len(pkg.Versions))])
	}
	for _, from := range heldUnder(s, name) {
		fmt.Fprintf(b, "  also held under                %s\n", from)
	}
}

// heldUnder is the names the registry has stopped holding that point at this one, which
// are the names a configuration may still be asking for.
func heldUnder(s *state.State, name string) []string {
	var names []string
	for from, to := range s.Renamed {
		if to == name {
			names = append(names, from)
		}
	}
	slices.Sort(names)
	return names
}
