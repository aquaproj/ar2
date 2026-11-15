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
// A package the registry holds nothing of yet comes first: see fresh. The registry is no
// use for a package until it has some version of it, while one it already serves gains
// little from the next ten versions of its history -- and every package already started
// takes a turn of up to the run's limit each lap, so a lap is mostly history, and the
// packages that haven't started waited behind all of it.
//
// After that, whoever has had the fewest turns comes first, so that a run picks up where
// the last one stopped and every package gets one before any package gets another. A run
// is bounded and the order was otherwise the same every time, which left the packages
// behind the cut waiting on the ones in front finishing -- and a package that takes a
// share and gets nowhere never finishes.
//
// Within that, stars come first because they approximate how many people an entry
// helps: a turn is shared out evenly, and who takes it first is not. A package
// without a GitHub repository goes last: its star count is unknown rather than zero,
// and sorting it among the zero-star packages would put it ahead of packages that are
// genuinely more used. Some popular packages are delayed by this, which is accepted.
func order(s *state.State) []*Candidate {
	candidates := make([]*Candidate, 0, len(s.Packages))
	for name, pkg := range s.Packages {
		candidates = append(candidates, &Candidate{Name: name, Package: pkg})
	}
	sortCandidates(candidates)
	return candidates
}

// sortCandidates puts the candidates in the order: see order.
func sortCandidates(candidates []*Candidate) {
	lap := slowestLap(candidates)
	slices.SortFunc(candidates, func(a, b *Candidate) int {
		return compare(lap, a, b)
	})
}

// slowestLap is the fewest turns any package whose history was walked has had, which is
// the lap the order is in. With no such package every package is fresh, and it is -1.
func slowestLap(candidates []*Candidate) int {
	lap := -1
	for _, c := range candidates {
		if c.Package.LastDeepCheck.IsZero() {
			continue
		}
		if lap == -1 || c.Package.Round < lap {
			lap = c.Package.Round
		}
	}
	return lap
}

// fresh says whether a package's history was never walked, and it has had no more turns
// than the slowest package that was.
//
// The second half is what keeps a package that fails every time -- its history is never
// walked, so it would stay fresh -- from taking every run. Its turn moves it a lap ahead
// of the rest and out of the front, and it is back there once the rest catch up: one try
// a lap, as it had before.
func fresh(lap int, pkg *state.Package) bool {
	return pkg.LastDeepCheck.IsZero() && (lap == -1 || pkg.Round <= lap)
}

// compare is the order itself: fresh packages first, then fewest turns, then the priority
// within a turn.
func compare(lap int, a, b *Candidate) int {
	if d := cmp.Compare(freshRank(lap, a.Package), freshRank(lap, b.Package)); d != 0 {
		return d
	}
	if d := cmp.Compare(a.Package.Round, b.Package.Round); d != 0 {
		return d
	}
	if d := cmp.Compare(rank(a.Package), rank(b.Package)); d != 0 {
		return d
	}
	if d := cmp.Compare(b.Package.Stars, a.Package.Stars); d != 0 {
		return d
	}
	// Ties are broken by name so that a run is reproducible: the same state must
	// produce the same work in the same order.
	return cmp.Compare(a.Name, b.Name)
}

// freshRank puts fresh packages ahead of the rest.
func freshRank(lap int, pkg *state.Package) int {
	if fresh(lap, pkg) {
		return 0
	}
	return 1
}

// rank puts packages with a known star count ahead of those without one.
func rank(pkg *state.Package) int {
	if pkg.RepoOwner == "" || pkg.RepoName == "" {
		return 1
	}
	return 0
}
