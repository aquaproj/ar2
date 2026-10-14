package g2_test

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

// A list behind its directory starts from what it says, reads only the versions it lacks,
// and drops the ones gone.
func TestClient_VersionsListFile_behind(t *testing.T) {
	t.Parallel()
	listPath := testDir + "/versions.json"
	git := &fakeGit{
		versionsTrees: map[string]string{"main": "now"},
		versionLists:  map[string][]string{"main": {"v1.0.0", "v2.0.0"}},
		files: map[string]string{
			// v1.0.0 is listed with a digest the file wouldn't hash to, so a list that
			// read it again would say otherwise.
			"main:" + listPath: `{"source":"before","versions":[` +
				`{"version":"v1.0.0","digest":"sha256:listed"},` +
				`{"version":"v0.9.0","digest":"sha256:gone"}]}`,
			"main:" + testDir + "/versions/v2.0.0/registry-1.json": `{"published_at":"2026-10-01T00:00:00Z","assets":[]}`,
		},
	}
	file, err := newTestClient(t, git).VersionsListFile(context.Background(), slog.New(slog.DiscardHandler), "main", testID)
	if err != nil {
		t.Fatal(err)
	}
	if file == nil || file.Path != listPath || file.Deleted {
		t.Fatalf("the file is %+v", file)
	}
	got := listed(t, file.Content, "now")
	if got["v1.0.0"] != " sha256:listed" {
		t.Errorf("v1.0.0 was read again: %q", got["v1.0.0"])
	}
	delete(got, "v1.0.0")
	if diff := cmp.Diff([]string{"v2.0.0"}, slices.Sorted(maps.Keys(got))); diff != "" {
		t.Errorf("the versions listed besides v1.0.0 (-want +got):\n%s", diff)
	}
	if !strings.HasPrefix(got["v2.0.0"], "2026-10-01T00:00:00Z ") {
		t.Errorf("v2.0.0 wasn't read from its file: %q", got["v2.0.0"])
	}
}

// A list made from the directory it is beside says nothing new.
func TestClient_VersionsListFile_upToDate(t *testing.T) {
	t.Parallel()
	git := &fakeGit{
		versionsTrees: map[string]string{"main": "now"},
		files:         map[string]string{"main:" + testDir + "/versions.json": `{"source":"now","versions":[]}`},
	}
	file, err := newTestClient(t, git).VersionsListFile(context.Background(), slog.New(slog.DiscardHandler), "main", testID)
	if err != nil {
		t.Fatal(err)
	}
	if file != nil {
		t.Errorf("a list in step changes: %+v", file)
	}
}

// A package with no versions left loses its list.
func TestClient_VersionsListFile_noVersions(t *testing.T) {
	t.Parallel()
	git := &fakeGit{
		files: map[string]string{"main:" + testDir + "/versions.json": `{"source":"before","versions":[]}`},
	}
	file, err := newTestClient(t, git).VersionsListFile(context.Background(), slog.New(slog.DiscardHandler), "main", testID)
	if err != nil {
		t.Fatal(err)
	}
	if file == nil || !file.Deleted {
		t.Errorf("the list stays: %+v", file)
	}
}

// listed reads a list made from the source given, as "<published_at> <digest>" by version.
func listed(t *testing.T, content, source string) map[string]string {
	t.Helper()
	list, err := aquag2.ReadVersions(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if list.Source != source {
		t.Errorf("the list is made from %q", list.Source)
	}
	got := map[string]string{}
	for _, v := range list.Versions {
		got[v.Version] = v.PublishedAt + " " + v.Digest
	}
	return got
}
