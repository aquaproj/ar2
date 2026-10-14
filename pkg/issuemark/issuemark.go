// Package issuemark records, in an issue ar2 writes, what the issue is about, in a form ar2
// can read back to find out whether it is resolved.
//
// An issue is prose for a person. What it is about -- which versions of which package, and
// for an environment left out, which environments -- is also written as a marker, a comment
// in the markdown that the rendered issue doesn't show, so that whether everything it is about
// has since been resolved can be asked of the registry rather than of somebody reading it.
package issuemark

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Kinds of issue.
const (
	// KindMissingEnvironment is an issue about environments a version was published without.
	KindMissingEnvironment = "missing-environment"
	// KindIntegrity is an issue about versions a check of the release's integrity stopped.
	KindIntegrity = "integrity"
	// KindTransientFailure is an issue about versions that keep failing for a reason that
	// should pass on another try.
	KindTransientFailure = "transient-failure"
)

// Record is what one issue or comment is about.
type Record struct {
	Kind    string `json:"kind"`
	Package string `json:"package"`
	// Versions are the versions it is about, each with the environments it is about as
	// os/arch, which an issue about integrity has none of: what was stopped is the version.
	Versions map[string][]string `json:"versions"`
}

const prefix = "<!-- ar2-issue: "

var markerPattern = regexp.MustCompile(`<!-- ar2-issue: (\{.*?\}) -->`)

// Marker renders the record as a comment the rendered markdown doesn't show.
func Marker(r *Record) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("marshal what the issue is about: %w", err)
	}
	return prefix + string(b) + " -->", nil
}

// Parse reads every record a text holds. A marker that can't be read is skipped: what it says
// is then nothing the issue can be closed over.
func Parse(text string) []*Record {
	var out []*Record
	for _, m := range markerPattern.FindAllStringSubmatch(text, -1) {
		r := &Record{}
		if err := json.Unmarshal([]byte(m[1]), r); err != nil || r.Kind == "" || r.Package == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
