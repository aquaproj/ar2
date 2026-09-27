package tidy

import "errors"

var (
	// errNoDefinition is returned for a named package whose branch holds none.
	errNoDefinition = errors.New("the package's branch holds no definition")
	// errNoBranch is returned when the package branch went between reading it and
	// committing to it.
	errNoBranch = errors.New("the package has no branch")
)
