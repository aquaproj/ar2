// Package tidy takes out of a package definition what a release can be read for.
//
// A definition is written once, by the conversion or by a person, and read by every
// generation after that. What it should say is the part a release can't answer -- which
// assets are the command, which of its files is the executable, who signs it -- because
// everything else is read off the release each time and a definition repeating it is a
// copy that can go out of date without anybody touching it.
//
// The definitions written before that was true still say the rest, so this takes it out.
package tidy

import (
	"fmt"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/asset"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// replacementsKey is the field that says how a release writes a platform.
const replacementsKey = "replacements"

// Replacements takes the spellings the parser works out for itself out of a definition,
// and says which ones went.
//
// The file is edited as a syntax tree rather than read into a definition and written back
// out. A definition on a package branch is a file a maintainer edits: the comment saying
// why a filter is there is the most valuable line in it, and re-rendering would drop it
// while claiming to have tidied up.
//
// Every replacements block is reached, wherever it is: the definition's own, each
// version_override's, and each override's within them. A block left with nothing goes
// altogether rather than staying as an empty map.
func Replacements(content string) (string, []string, error) {
	file, err := parser.ParseBytes([]byte(content), parser.ParseComments)
	if err != nil {
		return "", nil, fmt.Errorf("parse a package definition as YAML: %w", err)
	}
	if len(file.Docs) == 0 {
		return content, nil, nil
	}
	var removed []string
	for _, mapping := range mappings(file.Docs[0].Body) {
		removed = append(removed, trim(mapping)...)
	}
	if len(removed) == 0 {
		return content, nil, nil
	}
	// Exactly one newline at the end, whether or not the rendering kept the one the
	// file had.
	return strings.TrimSuffix(file.String(), "\n") + "\n", removed, nil
}

// mappings is every mapping node in the document, so that a replacements block is found
// wherever a definition puts one.
func mappings(node ast.Node) []*ast.MappingNode {
	var out []*ast.MappingNode
	switch t := node.(type) {
	case *ast.MappingNode:
		out = append(out, t)
		for _, value := range t.Values {
			out = append(out, mappings(value.Value)...)
		}
	case *ast.MappingValueNode:
		out = append(out, mappings(t.Value)...)
	case *ast.SequenceNode:
		for _, value := range t.Values {
			out = append(out, mappings(value)...)
		}
	}
	return out
}

// trim takes the known spellings out of the mapping's replacements, and the replacements
// out of the mapping when nothing is left. It returns what went, as "<platform>: <spelling>".
func trim(mapping *ast.MappingNode) []string {
	value := mappingValue(mapping, replacementsKey)
	if value == nil {
		return nil
	}
	replacements, ok := value.Value.(*ast.MappingNode)
	if !ok {
		// A single replacement parses as one mapping value rather than a mapping, and
		// what it says is the same thing.
		single, ok := value.Value.(*ast.MappingValueNode)
		if !ok {
			return nil
		}
		replacements = &ast.MappingNode{Values: []*ast.MappingValueNode{single}}
	}

	var removed []string
	kept := make([]*ast.MappingValueNode, 0, len(replacements.Values))
	for _, entry := range replacements.Values {
		platform, spelling := pair(entry)
		if platform == "" || !asset.KnowsSpelling(platform, spelling) {
			kept = append(kept, entry)
			continue
		}
		removed = append(removed, platform+": "+spelling)
	}
	if len(removed) == 0 {
		return nil
	}
	if len(kept) == 0 {
		mapping.Values = without(mapping.Values, value)
		return removed
	}
	replacements.Values = kept
	value.Value = replacements
	return removed
}

// mappingValue is the mapping's entry under the key, or nothing.
func mappingValue(mapping *ast.MappingNode, key string) *ast.MappingValueNode {
	for _, value := range mapping.Values {
		if name, ok := value.Key.(*ast.StringNode); ok && name.Value == key {
			return value
		}
	}
	return nil
}

// pair reads one replacement, and nothing when it isn't two strings.
func pair(entry *ast.MappingValueNode) (string, string) {
	key, ok := entry.Key.(*ast.StringNode)
	if !ok {
		return "", ""
	}
	spelling, ok := entry.Value.(*ast.StringNode)
	if !ok {
		return "", ""
	}
	return strings.ToLower(key.Value), spelling.Value
}

// without is the mapping's values with one of them gone.
func without(values []*ast.MappingValueNode, value *ast.MappingValueNode) []*ast.MappingValueNode {
	out := make([]*ast.MappingValueNode, 0, len(values))
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}
