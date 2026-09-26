package index

import "errors"

// errNoDefinition is returned for a package whose branch holds no definition to read
// an entry out of.
var errNoDefinition = errors.New("the package's branch holds no definition")
