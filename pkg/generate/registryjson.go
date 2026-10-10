// Package generate builds aqua-registry-g2's registry.json from an upstream release.
package generate

import (
	"encoding/json"
	"fmt"

	"github.com/aquaproj/aqua/v2/pkg/g2"
)

// Registry is the content of versions/<version>/registry.json, and Asset is one of
// its entries.
//
// The types are aqua's. aqua reads what ar2 writes, so the format is described where
// it is read; defining it again here would be two descriptions of one thing kept in
// step by hand.
type (
	Registry = g2.Registry
	Asset    = g2.Asset
	File     = g2.File
)

// Marshal renders registry.json the way it is stored: indented two spaces, with a newline
// at the end.
//
// It is read in pull requests, where a file on one line makes every change a change to the
// whole file, and a person deciding whether to replace what is published has to read which
// field moved. What the indentation costs is little: over the 1,858 files the registry held
// it was 43% more bytes raw but 7.9% gzipped, about 40 bytes a file, and gzipped is how aqua
// downloads it and how git stores it.
//
// Every writer of registry.json goes through this, so a file generated again comes out the
// same bytes as the one it would replace.
func Marshal(reg *Registry) ([]byte, error) {
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal registry.json: %w", err)
	}
	return append(b, '\n'), nil
}
