package g2

import (
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// CatalogueFiles renders the files the catalogue is made of.
//
// The table that resolves a name is written with it rather than beside it. It is the
// catalogue read the other way round -- a name, and every name a package used to have, to
// where the package is -- so rendering them together is what keeps them from describing
// different registries.
func CatalogueFiles(index *aquag2.Index) ([]*File, error) {
	indexContent, err := index.Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names what it failed to render
	}
	namesContent, err := aquag2.NewNames(index).Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	return []*File{
		{Path: aquag2.IndexFileName, Content: indexContent},
		{Path: aquag2.NamesFileName, Content: namesContent},
	}, nil
}
