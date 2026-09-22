// Package run implements the logic behind the 'ar2 run' command.
//
// A full backfill can't be done in one invocation, so each one takes a bounded
// amount of work and records what it did. The order decides what gets generated
// first, and it is deliberate: the registry is most useful when the packages people
// actually install are in it.
package run

import (
	"cmp"
	"slices"

	"github.com/aquaproj/ar2/pkg/state"
)

// Candidate is one package to work on, in the order it should be worked on.
type Candidate struct {
	Name    string
	Package *state.Package
}

// order returns the packages to process, most important first.
//
// Stars come first because they approximate how many people an entry helps. A
// package without a GitHub repository goes last: its star count is unknown rather
// than zero, and sorting it among the zero-star packages would put it ahead of
// packages that are genuinely more used. Some popular packages are delayed by this,
// which is accepted.
func order(s *state.State) []*Candidate {
	candidates := make([]*Candidate, 0, len(s.Packages))
	for name, pkg := range s.Packages {
		candidates = append(candidates, &Candidate{Name: name, Package: pkg})
	}
	slices.SortFunc(candidates, func(a, b *Candidate) int {
		if d := cmp.Compare(rank(a.Package), rank(b.Package)); d != 0 {
			return d
		}
		if d := cmp.Compare(b.Package.Stars, a.Package.Stars); d != 0 {
			return d
		}
		// Ties are broken by name so that a run is reproducible: the same state must
		// produce the same work in the same order.
		return cmp.Compare(a.Name, b.Name)
	})
	return candidates
}

// rank puts packages with a known star count ahead of those without one.
func rank(pkg *state.Package) int {
	if pkg.RepoOwner == "" || pkg.RepoName == "" {
		return 1
	}
	return 0
}
