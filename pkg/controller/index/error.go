package index

import "errors"

var (
	// errNoDefinition is returned for a package whose branch holds no definition to read
	// an entry out of.
	errNoDefinition = errors.New("the package's branch holds no definition")
	// errNoDefinitions is returned when a Controller that can't read the branches is
	// asked to reconcile. Whoever built it didn't mean to.
	errNoDefinitions = errors.New("the controller has no reader of the package branches")
)
