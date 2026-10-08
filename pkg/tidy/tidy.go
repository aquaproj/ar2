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

const (
	// replacementsKey is the field that says how a release writes a platform.
	replacementsKey = "replacements"
	// checksumKey is the field that says where the checksum file is and how it is signed.
	checksumKey = "checksum"
	// overridesKey is the field that says what an environment does differently.
	overridesKey = "overrides"
	// typeKey is the field that says where a package comes from.
	typeKey = "type"
	// typeGitHubRelease is the type whose assets are what the spellings are read off.
	typeGitHubRelease = "github_release"
)

// selectorKeys are what an override says about which environment it is for.
//
// variants is not one of them. An override carrying variants says that the variant exists
// at all, which is what makes an entry for it generated: claude-code's linux/glibc entry
// is there because the definition names glibc beside musl, and the override naming it says
// nothing else.
var selectorKeys = map[string]struct{}{ //nolint:gochecknoglobals
	"goos":   {},
	"goarch": {},
	"envs":   {},
}

// Removed is what a tidying took out of a definition.
type Removed struct {
	// Spellings are the replacements that went, as "<platform>: <spelling>".
	Spellings []string
	// Checksums is how many checksum blocks went.
	Checksums int
	// Overrides is how many overrides went for saying nothing.
	Overrides int
}

// Any reports whether anything went.
func (r *Removed) Any() bool {
	return len(r.Spellings) > 0 || r.Checksums > 0 || r.Overrides > 0
}

// Definition takes out of a definition what it doesn't have to say: the spellings the
// parser works out for itself, where the checksum file is, and an override left saying
// nothing but which environment it is for. It says what went.
//
// The file is edited as a syntax tree rather than read into a definition and written back
// out. A definition on a package branch is a file a maintainer edits: the comment saying
// why a filter is there is the most valuable line in it, and re-rendering would drop it
// while claiming to have tidied up.
//
// Every replacements block is reached, wherever it is: the definition's own, each
// version_override's, and each override's within them. A block left with nothing goes
// altogether rather than staying as an empty map.
func Definition(content string) (string, *Removed, error) {
	file, err := parser.ParseBytes([]byte(content), parser.ParseComments)
	if err != nil {
		return "", nil, fmt.Errorf("parse a package definition as YAML: %w", err)
	}
	removed := &Removed{}
	if len(file.Docs) == 0 {
		return content, removed, nil
	}
	nodes := mappings(file.Docs[0].Body)
	// The spellings are read off the asset names, so they are the parser's to work out
	// only where there are asset names: a package the registry downloads by a URL puts
	// the platform into that URL through the replacements, and nothing else in the
	// definition says what it should be. Taking them out of one leaves a URL asking for
	// darwin/amd64 where the release publishes darwin/x64.
	spellings := releasedOnGitHub(nodes)
	for _, mapping := range nodes {
		if spellings {
			removed.Spellings = append(removed.Spellings, trim(mapping)...)
		}
		if dropChecksum(mapping) {
			removed.Checksums++
		}
	}
	// After the fields rather than with them, because an override saying nothing is what
	// taking the last field out of one leaves behind.
	for _, mapping := range nodes {
		removed.Overrides += dropEmptyOverrides(mapping)
	}
	if !removed.Any() {
		return content, removed, nil
	}
	// Exactly one newline at the end, whether or not the rendering kept the one the
	// file had.
	return strings.TrimSuffix(file.String(), "\n") + "\n", removed, nil
}

// releasedOnGitHub reports whether every type the definition names is a GitHub release.
//
// Every type, because a version_override can name another one: a package that was
// published somewhere else before it had releases says so there, and the replacements that
// era needs are the ones in it.
func releasedOnGitHub(nodes []*ast.MappingNode) bool {
	found := false
	for _, mapping := range nodes {
		value := mappingValue(mapping, typeKey)
		if value == nil {
			continue
		}
		scalar, ok := value.Value.(ast.ScalarNode)
		if !ok {
			return false
		}
		if fmt.Sprint(scalar.GetValue()) != typeGitHubRelease {
			return false
		}
		found = true
	}
	return found
}

// dropChecksum takes the checksum file out of the mapping, and says whether there was
// one.
//
// aqua-registry verifies an asset against that file. Here every entry carries the digest
// of the asset itself, taken when it was generated and checked again by the branch's CI
// on a machine of the environment the entry is for, so nothing ever reads the file --
// not even to know it exists. A definition saying where it is, and how its signature is
// checked, says it to nobody.
func dropChecksum(mapping *ast.MappingNode) bool {
	value := mappingValue(mapping, checksumKey)
	if value == nil {
		return false
	}
	mapping.Values = without(mapping.Values, value)
	return true
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

// dropEmptyOverrides takes out the overrides that say nothing but which environment they
// are for, and returns how many went.
//
// One is what taking a field out of an override leaves behind: "- goos: windows" and
// nothing else, which matches Windows and then applies nothing to it. A reader has to work
// out that it does nothing, which is worse than its not being there.
//
// An override with a later sibling that the same environment could match stays, because
// the first match is the one applied: taking it out would hand that environment to the
// sibling. version_overrides are left alone altogether -- an entry with nothing but its
// constraint says that those versions take the definition as it is, which is what stops an
// older era below it from answering for them.
func dropEmptyOverrides(mapping *ast.MappingNode) int {
	value := mappingValue(mapping, overridesKey)
	if value == nil {
		return 0
	}
	seq, ok := value.Value.(*ast.SequenceNode)
	if !ok {
		return 0
	}
	removed := 0
	kept := make([]ast.Node, 0, len(seq.Values))
	for i, entry := range seq.Values {
		if saysOnlyWhere(entry) && !matchedLater(entry, seq.Values[i+1:]) {
			removed++
			continue
		}
		kept = append(kept, entry)
	}
	if removed == 0 {
		return 0
	}
	if len(kept) == 0 {
		mapping.Values = without(mapping.Values, value)
		return removed
	}
	seq.Values = kept
	return removed
}

// saysOnlyWhere reports whether the override says which environment it is for and nothing
// more.
func saysOnlyWhere(node ast.Node) bool {
	values := entries(node)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		key, ok := value.Key.(*ast.StringNode)
		if !ok {
			return false
		}
		if _, ok := selectorKeys[key.Value]; !ok {
			return false
		}
	}
	return true
}

// matchedLater reports whether any of the overrides below this one could be reached by an
// environment it matches.
func matchedLater(node ast.Node, later []ast.Node) bool {
	for _, sibling := range later {
		if overlaps(node, sibling) {
			return true
		}
	}
	return false
}

// overlaps reports whether one environment could match both overrides.
//
// A later override's variants don't stop it: a machine carrying that variant matches the
// earlier override too, since that one asks for none. An envs list is a set of
// environments rather than a pair of fields, and whether two of them are disjoint isn't
// worth working out to save a line a person will read either way, so it counts as
// overlapping.
func overlaps(a, b ast.Node) bool {
	aOS, aArch, aEnvs := where(a)
	bOS, bArch, bEnvs := where(b)
	if aEnvs || bEnvs {
		return true
	}
	return sameOrEither(aOS, bOS) && sameOrEither(aArch, bArch)
}

// where is what the override says about which environment it is for, whatever else it says.
func where(node ast.Node) (string, string, bool) {
	var goos, goarch string
	var envs bool
	for _, value := range entries(node) {
		key, ok := value.Key.(*ast.StringNode)
		if !ok {
			continue
		}
		switch key.Value {
		case "goos":
			goos = scalar(value.Value)
		case "goarch":
			goarch = scalar(value.Value)
		case "envs":
			envs = true
		}
	}
	return goos, goarch, envs
}

// sameOrEither reports whether the two say the same thing, or one of them says nothing and
// so covers what the other says.
func sameOrEither(a, b string) bool {
	return a == "" || b == "" || a == b
}

// scalar is a node's value when it is a string, and nothing when it is anything else --
// which counts as saying nothing, so an override written in a way this can't read is left
// where it is.
func scalar(node ast.Node) string {
	if s, ok := node.(*ast.StringNode); ok {
		return s.Value
	}
	return ""
}

// entries is a mapping's values, for a node that may be a mapping of several keys or of
// one: a sequence entry with a single key parses as one mapping value rather than as a
// mapping.
func entries(node ast.Node) []*ast.MappingValueNode {
	switch t := node.(type) {
	case *ast.MappingNode:
		return t.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{t}
	}
	return nil
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
