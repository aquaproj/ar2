package tidy

import "errors"

var (
	// errNoDefinition is returned for a named package that has none.
	errNoDefinition = errors.New("the package holds no definition")
	// errNoBranch is returned for a package the registry doesn't hold.
	errNoBranch = errors.New("the registry holds no such package")
)
