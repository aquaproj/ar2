package validateindex

import "errors"

var (
	errTrailingContent = errors.New("the catalogue holds more than one JSON document")
	errNoPackages      = errors.New("the catalogue has no packages list")
	errInvalid         = errors.New("the catalogue isn't what aqua reads it for")
)
