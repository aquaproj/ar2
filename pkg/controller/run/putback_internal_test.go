package run

import (
	"testing"
	"time"

	"github.com/aquaproj/ar2/pkg/state"
)

// A package too slow to download waits a lap, then two, then four, up to a limit.
func TestPutBack(t *testing.T) {
	t.Parallel()
	pkg := &state.Package{Round: 10}
	for _, want := range []int{1, 2, 4, 8, 16, 32, 32} {
		putBack(pkg)
		if pkg.PutBack != want {
			t.Fatalf("waits %d laps, want %d", pkg.PutBack, want)
		}
		if pkg.WaitUntil != pkg.Round+want {
			t.Fatalf("waits until turn %d, want %d", pkg.WaitUntil, pkg.Round+want)
		}
	}
}

// A run with no time given never runs out of it.
func TestTimeUp(t *testing.T) {
	t.Parallel()
	if timeUp(&Input{}) {
		t.Error("a run with no time limit is out of time")
	}
	if !timeUp(&Input{Until: time.Now().Add(-time.Second)}) {
		t.Error("a run past its time isn't out of it")
	}
	if timeUp(&Input{Until: time.Now().Add(time.Hour)}) {
		t.Error("a run with time left is out of it")
	}
}
