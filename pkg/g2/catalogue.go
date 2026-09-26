package g2

import (
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// CatalogueFiles renders the files the catalogue is made of.
//
// The table of other names is written with it rather than beside it. It holds the same
// aliases the catalogue does, inverted into something a name can be looked up in, so
// rendering them together is what keeps them from describing different registries.
func CatalogueFiles(index *aquag2.Index) ([]*File, error) {
	indexContent, err := index.Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names what it failed to render
	}
	aliasContent, err := aquag2.NewAliases(index).Marshal()
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	return []*File{
		{Path: aquag2.IndexFileName, Content: indexContent},
		{Path: aquag2.AliasesFileName, Content: aliasContent},
	}, nil
}
