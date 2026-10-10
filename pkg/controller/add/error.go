package add

import "errors"

// errNoRepo is returned when the repository can't be read out of the package name and
// wasn't given.
var errNoRepo = errors.New(`the repository must be "<owner>/<name>"`)

// errNoBranch says the package has no id after it was taken over, which is a registry that
// didn't take the package over rather than something about the package.
var errNoBranch = errors.New("the registry holds no id for the package")
