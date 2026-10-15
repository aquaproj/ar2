package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// A draft isn't public, so it is no version. A prerelease is one, marked as one, and the
// latest release says whether the repository has published anything else.
func TestReleaseVersions(t *testing.T) {
	t.Parallel()
	r := &repository{}
	if err := json.Unmarshal([]byte(`{"latestRelease":{"tagName":"v3.0.0"},"releases":{"nodes":[
		{"tagName":"v3.0.0","isDraft":false,"isPrerelease":false},
		{"tagName":"v3.0.0-rc.1","isDraft":false,"isPrerelease":true},
		{"tagName":"v2.9.0","isDraft":true,"isPrerelease":false},
		{"tagName":"v2.8.0","isDraft":false,"isPrerelease":false}
	]}}`), r); err != nil {
		t.Fatal(err)
	}
	want := &Found{
		Versions:    []string{"v3.0.0", "v3.0.0-rc.1", "v2.8.0"},
		Prereleases: map[string]struct{}{"v3.0.0-rc.1": {}},
		Stable:      true,
	}
	if diff := cmp.Diff(want, releaseVersions(r)); diff != "" {
		t.Errorf("the versions are wrong (-want +got):\n%s", diff)
	}
}

// A repository whose alias wasn't answered has no versions to read, which is not
// something to trip over.
func TestReleaseVersions_nothingAnswered(t *testing.T) {
	t.Parallel()
	if got := releaseVersions(&repository{}).Versions; got != nil {
		t.Errorf("got %v, want nothing", got)
	}
	if got := tagNames(&repository{}).Versions; got != nil {
		t.Errorf("got %v, want nothing", got)
	}
}

// The prereleases are picked when the package asks for them or the repository has
// published nothing else, and left out otherwise.
func TestFound_Pick(t *testing.T) {
	t.Parallel()
	pre := map[string]struct{}{"v2.0.0-rc.1": {}}
	for _, d := range []struct {
		name       string
		found      *Found
		prerelease bool
		want       []string
	}{
		{
			name:  "a release beside them",
			found: &Found{Versions: []string{"v2.0.0-rc.1", "v1.0.0"}, Prereleases: pre, Stable: true},
			want:  []string{"v1.0.0"},
		},
		{
			name:       "the package asks for them",
			found:      &Found{Versions: []string{"v2.0.0-rc.1", "v1.0.0"}, Prereleases: pre, Stable: true},
			prerelease: true,
			want:       []string{"v2.0.0-rc.1", "v1.0.0"},
		},
		{
			name:  "nothing but prereleases",
			found: &Found{Versions: []string{"v2.0.0-rc.1"}, Prereleases: pre},
			want:  []string{"v2.0.0-rc.1"},
		},
		{
			// Not a repository with no version: one that wasn't read.
			name: "nothing found",
		},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(d.want, d.found.Pick(d.prerelease)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// A repository with no release is read by its tags, and one written as a prerelease is
// marked as one; one with releases is read by them.
func TestReleasesOrTags(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		name string
		body string
		want *Found
	}{
		{
			name: "no release",
			body: `{"releases":{"nodes":[]},"refs":{"nodes":[
				{"name":"v4.3.0-rc2"},{"name":"v4.2.0"},{"name":"stable"}
			]}}`,
			want: &Found{
				Versions:    []string{"v4.3.0-rc2", "v4.2.0", "stable"},
				Prereleases: map[string]struct{}{"v4.3.0-rc2": {}},
				Stable:      true,
			},
		},
		{
			name: "no release, and every tag a prerelease",
			body: `{"releases":{"nodes":[]},"refs":{"nodes":[{"name":"v0.1.0-alpha"}]}}`,
			want: &Found{
				Versions:    []string{"v0.1.0-alpha"},
				Prereleases: map[string]struct{}{"v0.1.0-alpha": {}},
			},
		},
		{
			name: "releases",
			body: `{"releases":{"nodes":[
				{"tagName":"v2.0.0-rc.1","isPrerelease":true}
			]},"refs":{"nodes":[{"name":"v1.0.0"}]}}`,
			want: &Found{
				Versions:    []string{"v2.0.0-rc.1"},
				Prereleases: map[string]struct{}{"v2.0.0-rc.1": {}},
			},
		},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			r := &repository{}
			if err := json.Unmarshal([]byte(d.body), r); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(d.want, releasesOrTags(r)); diff != "" {
				t.Errorf("the versions are wrong (-want +got):\n%s", diff)
			}
		})
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
		"latestRelease { tagName }",
		"nameWithOwner",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("the releases selection is missing %q:\n%s", want, rel)
		}
	}
	both := releasesAndTags("r2o", "r2n")
	for _, want := range []string{
		"releases(first: 10, orderBy: {field: CREATED_AT, direction: DESC})",
		`refs(refPrefix: "refs/tags/", first: 10, orderBy: {field: TAG_COMMIT_DATE, direction: DESC})`,
	} {
		if !strings.Contains(both, want) {
			t.Errorf("the releases and tags selection is missing %q:\n%s", want, both)
		}
	}
	tag := tags("r1o", "r1n")
	for _, want := range []string{
		`refs(refPrefix: "refs/tags/", first: 10, orderBy: {field: TAG_COMMIT_DATE, direction: DESC})`,
		"nodes { name }",
		"nameWithOwner",
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the tags selection is missing %q:\n%s", want, tag)
		}
	}
}

// The sweep asks every repository what it is called, and GitHub answers a query made
// with an old name, so a repository that has been renamed or transferred since the
// registry recorded it is found without asking anything extra.
func TestClient_Versions_renamed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{
			"r0":{"nameWithOwner":"anomalyco/opencode","releases":{"nodes":[{"tagName":"v1.0.0"}]}},
			"r1":{"nameWithOwner":"cli/cli","releases":{"nodes":[{"tagName":"v2.1.0"}]}}
		}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	c.endpoint = srv.URL

	got, err := c.Versions(t.Context(), []Repo{
		{Owner: "sst", Name: "opencode"},
		{Owner: "cli", Name: "cli"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keyed by the name that was asked for, throughout.
	versions := map[string][]string{}
	for repo, found := range got.Versions {
		versions[repo] = found.Versions
	}
	if diff := cmp.Diff(map[string][]string{
		"sst/opencode": {"v1.0.0"},
		"cli/cli":      {"v2.1.0"},
	}, versions); diff != "" {
		t.Errorf("the versions are wrong (-want +got):\n%s", diff)
	}
	// Only the one that answers to another name is named, so the caller has the list
	// of what the registry is out of date about rather than every package.
	if diff := cmp.Diff(map[string]string{
		"sst/opencode": "anomalyco/opencode",
	}, got.Names); diff != "" {
		t.Errorf("the renames are wrong (-want +got):\n%s", diff)
	}
}

// GitHub answers a query made in any case and replies with the names as they are spelled,
// so the owner's own capitalisation comes back. That is the same repository, not a rename.
func TestRenamed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		asked, answered string
		want            bool
	}{
		{asked: "Arriven/db1000n", answered: "arriven/db1000n", want: false},
		{asked: "CrociDB/bulletty", answered: "crocidb/bulletty", want: false},
		{asked: "sst/opencode", answered: "anomalyco/opencode", want: true},
		{asked: "mgechev/revive", answered: "revive-lint/revive", want: true},
		{asked: "cli/cli", answered: "cli/cli", want: false},
		// A repository that couldn't be read says nothing about its name.
		{asked: "cli/cli", answered: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.asked+" -> "+tt.answered, func(t *testing.T) {
			t.Parallel()
			if got := renamed(tt.asked, tt.answered); got != tt.want {
				t.Errorf("renamed(%q, %q) = %v, want %v", tt.asked, tt.answered, got, tt.want)
			}
		})
	}
}
