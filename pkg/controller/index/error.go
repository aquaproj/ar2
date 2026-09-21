package index

import "errors"

// errNotAPackageBranch is what a branch that doesn't hold a package gets. The
// workflow passes whichever ref was created, so this is the ordinary way a tag or an
// operational branch is turned away.
var errNotAPackageBranch = errors.New("the branch doesn't hold a package")
