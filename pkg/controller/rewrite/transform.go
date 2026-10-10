// Package rewrite brings the files the registry has published to the form a generation
// writes now, once (aquaproj/aqua-registry-g2#743).
//
// Each definition gains its repository's id, and every registry.json is rendered again:
// the id on its entries, cosign's command line moved into the fields that say it, indented.
// Nothing about a release is read again. What changes is how the same facts are written,
// which is why the result can be checked by deriving it again from what it replaced,
// rather than by downloading every asset a second time.
package rewrite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
)

var (
	errOtherRepoID = errors.New("the definition records another repository id")
	errNoRepoName  = errors.New("the definition has no top-level repo_name to put repo_id after")
)

// Definition returns the definition with repo_id right after repo_name, the way
// g2.MarshalConfig writes one. The rest of the file is left as it is -- its comments are
// what somebody wrote down about the package -- so the id is inserted as a line rather than
// the file rendered again.
//
// A definition already recording the id is returned as it is, and one recording another id
// is an error: that is a repository taken over, for a person to look at.
func Definition(content string, id int64) (string, error) {
	lines := strings.SplitAfter(content, "\n")
	want := strconv.FormatInt(id, 10)
	after := -1
	for i, line := range lines {
		if value, ok := topLevel(line, "repo_id"); ok {
			if value != want {
				return "", fmt.Errorf("%w: %s, not %s", errOtherRepoID, value, want)
			}
			return content, nil
		}
		if _, ok := topLevel(line, "repo_name"); ok && after < 0 {
			after = i
		}
	}
	if after < 0 {
		return "", errNoRepoName
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:after+1]...)
	out = append(out, "repo_id: "+want+"\n")
	out = append(out, lines[after+1:]...)
	return strings.Join(out, ""), nil
}

// topLevel reads a top-level key's value out of a line, and reports false for any other
// line.
func topLevel(line, key string) (string, bool) {
	value, ok := strings.CutPrefix(line, key+":")
	if !ok {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(value), `"'`), true
}

// File renders a published registry.json again the way a generation writes it now: the
// definition's repository id on the entries naming that repository, cosign's command line
// moved into the fields that say it, indented.
//
// It is read strictly. A field the type doesn't know would be dropped by rendering, and
// dropping what a published file says is the one thing this must not do.
func File(content string, cfg *aquag2.Config, version string) (string, error) {
	reg := &generate.Registry{}
	decoder := json.NewDecoder(bytes.NewReader([]byte(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(reg); err != nil {
		return "", fmt.Errorf("read registry.json: %w", err)
	}
	generate.StampRepoID(reg, cfg)
	for _, asset := range reg.Assets {
		sign.Structure(asset, version)
	}
	b, err := generate.Marshal(reg)
	if err != nil {
		return "", err //nolint:wrapcheck // the error says what it couldn't marshal
	}
	return string(b), nil
}
