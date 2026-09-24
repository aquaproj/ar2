package test

import "errors"

var (
	errNoVersionInPath = errors.New("the path isn't versions/<version>/registry-1.json, so the version can't be read from it")
	errTrailingContent = errors.New("the file holds more than one JSON document")
	errNoAsset         = errors.New("the registry holds no asset")
	errNoEnv           = errors.New("the entry names no os or arch")
	errNoType          = errors.New("the entry names no type")
	errNoChecksum      = errors.New("the entry carries no checksum")
	errChecksum        = errors.New("the checksum doesn't match the asset")
	errFilesMissing    = errors.New("files aren't in the archive")
	errFilesMoved      = errors.New("files are in the archive under other paths")
)
