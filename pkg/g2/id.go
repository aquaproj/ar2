package g2

import (
	"strconv"
	"time"
)

// MintID returns an identifier for a package that no other package has.
//
// A package's directory is named after this rather than after the package, because a name is
// not an identity: a repository can be renamed, and the name it leaves behind can be taken
// by a different repository. What the identifier has to be is unchanging, unique, and
// mintable without asking anybody -- not random, and not meaningful.
//
// So it is the time it was minted, in seconds. Ten characters, in an order that is the
// order packages were taken over, and readable with `date -r`. The randomness a UUID
// carries would be 122 bits spent on a few thousand identifiers that are minted one at a
// time, and it would be paid for in the table a consumer resolves through: high-entropy
// strings don't compress, and there is one per package in it.
//
// The layout depends on it too. A package's directory is sharded by the id's last two
// digits (see PackageDir), which spread evenly only because they are the low digits of a
// clock: an id minted any other way -- rounded, or counted from a round number -- would
// pile packages into a few shards.
//
// Uniqueness doesn't rest on the clock. Whoever mints holds the catalogue, so taken says
// which identifiers are already spoken for, and a second that is taken is stepped past.
// Nothing else needs a lock: what would make two packages share a branch is two identical
// identifiers, and that is a question this can answer before it answers anything else.
func MintID(now time.Time, taken map[string]struct{}) string {
	for sec := now.Unix(); ; sec++ {
		id := strconv.FormatInt(sec, 10)
		if _, ok := taken[id]; !ok {
			return id
		}
	}
}
