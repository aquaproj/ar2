package g2_test

import (
	"testing"
	"time"

	"github.com/aquaproj/ar2/pkg/g2"
)

// The identifier is the second it was minted in, so it reads as a time and sorts in the
// order packages were taken over.
func TestMintID(t *testing.T) {
	t.Parallel()
	if got := g2.MintID(time.Unix(1790000000, 0), nil); got != "1790000000" {
		t.Fatalf("got %s", got)
	}
}

// Uniqueness doesn't rest on the clock. Whoever mints holds the catalogue, so a second
// already spoken for is stepped past -- which is what minting twice without the clock
// moving comes to.
func TestMintID_stepsPastWhatIsTaken(t *testing.T) {
	t.Parallel()
	now := time.Unix(1790000000, 0)
	taken := map[string]struct{}{"1790000000": {}, "1790000001": {}}
	got := g2.MintID(now, taken)
	if got != "1790000002" {
		t.Fatalf("got %s", got)
	}
}
