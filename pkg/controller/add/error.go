package add

import "errors"

// errNoRepo is returned when the repository can't be read out of the package name and
// wasn't given.
var errNoRepo = errors.New(`the repository must be "<owner>/<name>"`)
