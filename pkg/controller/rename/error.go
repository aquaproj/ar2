package rename

import "errors"

// errSameName says a rename names one package twice, which is nothing to do rather than
// something to do carefully.
var errSameName = errors.New("the package is already called that")
