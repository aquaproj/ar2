// Package generate builds aqua-registry-g2's registry.json from an upstream release.
package generate

import "github.com/aquaproj/aqua/v2/pkg/g2"

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
