package show

import "errors"

// errNotFound is returned when the catalogue holds no package under the name or
// identifier asked for.
var errNotFound = errors.New("the catalogue holds no such package")
