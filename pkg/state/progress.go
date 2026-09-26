package state

import "math"

// Progress is how far the registry has got, as the state sees it.
//
// What the repository holds is the registry; this is what the order knows about getting
// there, which nothing else can answer. Counting branches says how many packages have
// anything at all, and says nothing about the ones still waiting for a turn or the ones
// whose history hasn't been walked.
type Progress struct {
	// Packages is how many packages are in the order.
	Packages int
	// CaughtUp is how many hold every version their last sweep saw upstream.
	CaughtUp int
	// Untouched is how many no run has reached yet.
	Untouched int
	// Lap is the fewest turns any package has had, which is the lap the order is on.
	Lap int
	// Waiting is how many are still on that lap, so a run reaches them before anything
	// gets another turn.
	Waiting int
	// Unwalked is how many have never had their whole history walked. A sweep sees the
	// newest versions, so a release dated in the past is only found by walking.
	Unwalked int
	// Renamed is how many names the registry has stopped holding, each pointing at the
	// one it holds now.
	Renamed int
}

// Progress reports how far the registry has got.
func (s *State) Progress() *Progress {
	p := &Progress{
		Packages: len(s.Packages),
		Renamed:  len(s.Renamed),
		Lap:      math.MaxInt,
	}
	for _, pkg := range s.Packages {
		if pkg == nil {
			continue
		}
		if pkg.CaughtUp {
			p.CaughtUp++
		}
		if pkg.Round == 0 {
			p.Untouched++
		}
		if pkg.LastDeepCheck.IsZero() {
			p.Unwalked++
		}
		p.Lap = min(p.Lap, pkg.Round)
	}
	if p.Lap == math.MaxInt {
		p.Lap = 0
	}
	for _, pkg := range s.Packages {
		if pkg != nil && pkg.Round == p.Lap {
			p.Waiting++
		}
	}
	return p
}
