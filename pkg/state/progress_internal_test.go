package state

import (
	"testing"
	"time"
)

// The lap is the fewest turns anything has had, and what is waiting for it is what a run
// reaches before anything gets another turn.
func TestState_Progress(t *testing.T) {
	t.Parallel()
	s := New()
	s.Packages["a"] = &Package{Round: 4, CaughtUp: true, LastDeepCheck: time.Now()}
	s.Packages["b"] = &Package{Round: 3}
	s.Packages["c"] = &Package{Round: 3, CaughtUp: true}
	s.Renamed = map[string]string{"old": "a"}

	p := s.Progress()
	if p.Packages != 3 {
		t.Errorf("packages %d", p.Packages)
	}
	if p.CaughtUp != 2 {
		t.Errorf("caught up %d", p.CaughtUp)
	}
	if p.Lap != 3 {
		t.Errorf("lap %d", p.Lap)
	}
	if p.Waiting != 2 {
		t.Errorf("waiting %d", p.Waiting)
	}
	if p.Untouched != 0 {
		t.Errorf("untouched %d", p.Untouched)
	}
	if p.Unwalked != 2 {
		t.Errorf("unwalked %d", p.Unwalked)
	}
	if p.Renamed != 1 {
		t.Errorf("renamed %d", p.Renamed)
	}
}

// An empty state is what the first run works from, and its lap is zero rather than the
// largest number there is.
func TestState_Progress_empty(t *testing.T) {
	t.Parallel()
	p := New().Progress()
	if p.Lap != 0 || p.Packages != 0 || p.Waiting != 0 {
		t.Errorf("got %+v", p)
	}
}
