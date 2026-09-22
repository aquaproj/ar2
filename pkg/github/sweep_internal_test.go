package github

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// A draft isn't public and a prerelease isn't what a registry offers, which is the
// same rule the REST path applies.
func TestReleaseVersions(t *testing.T) {
	t.Parallel()
	r := &repository{}
	if err := json.Unmarshal([]byte(`{"releases":{"nodes":[
		{"tagName":"v3.0.0","isDraft":false,"isPrerelease":false},
		{"tagName":"v3.0.0-rc.1","isDraft":false,"isPrerelease":true},
		{"tagName":"v2.9.0","isDraft":true,"isPrerelease":false},
		{"tagName":"v2.8.0","isDraft":false,"isPrerelease":false}
	]}}`), r); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"v3.0.0", "v2.8.0"}, releaseVersions(r)); diff != "" {
		t.Errorf("the versions are wrong (-want +got):\n%s", diff)
	}
}

// A repository whose alias wasn't answered has no releases to read, which is not
// something to trip over.
func TestReleaseVersions_nothingAnswered(t *testing.T) {
	t.Parallel()
	if got := releaseVersions(&repository{}); got != nil {
		t.Errorf("got %v, want nothing", got)
	}
	if got := tagNames(&repository{}); got != nil {
		t.Errorf("got %v, want nothing", got)
	}
}

// The selections are written by hand, so a typo in them is a query GitHub rejects
// for every repository at once.
func TestSelections(t *testing.T) {
	t.Parallel()
	rel := releases("r0o", "r0n")
	for _, want := range []string{
		"repository(owner: $r0o, name: $r0n)",
		"releases(first: 10, orderBy: {field: CREATED_AT, direction: DESC})",
		"nodes { tagName isDraft isPrerelease }",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("the releases selection is missing %q:\n%s", want, rel)
		}
	}
	tag := tags("r1o", "r1n")
	for _, want := range []string{
		`refs(refPrefix: "refs/tags/", first: 10, orderBy: {field: TAG_COMMIT_DATE, direction: DESC})`,
		"nodes { name }",
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the tags selection is missing %q:\n%s", want, tag)
		}
	}
}
