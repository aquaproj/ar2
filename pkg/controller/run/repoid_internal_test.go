package run

import (
	"errors"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
)

func repoConfig(repoID int64) *aquag2.Config {
	return &aquag2.Config{
		PackageInfo: &aquaregistry.PackageInfo{RepoOwner: "cli", RepoName: "cli"},
		RepoID:      repoID,
	}
}

// A new definition takes the repository's id; one that records an id is held to it; one the
// registry already holds without an id is left for the rewrite of what is published.
func TestCheckRepoID(t *testing.T) {
	t.Parallel()
	c := &Controller{repoIDs: map[string]int64{"cli/cli": 212613049}}

	fresh := &definition{config: repoConfig(0)}
	if err := c.checkRepoID(fresh, "cli/cli"); err != nil || fresh.config.RepoID != 212613049 {
		t.Errorf("a new definition: id %d, error %v", fresh.config.RepoID, err)
	}
	held := &definition{config: repoConfig(0), held: true}
	if err := c.checkRepoID(held, "cli/cli"); err != nil || held.config.RepoID != 0 {
		t.Errorf("a held definition without an id: id %d, error %v", held.config.RepoID, err)
	}
	same := &definition{config: repoConfig(212613049), held: true}
	if err := c.checkRepoID(same, "cli/cli"); err != nil {
		t.Errorf("the same id: %v", err)
	}
	other := &definition{config: repoConfig(1), held: true}
	if err := c.checkRepoID(other, "cli/cli"); !errors.Is(err, errRepoReplaced) {
		t.Errorf("another id: %v, want errRepoReplaced", err)
	}
	unread := &definition{config: repoConfig(1), held: true}
	if err := c.checkRepoID(unread, "nobody/swept"); err != nil {
		t.Errorf("a repository the sweep didn't read: %v", err)
	}
}

// Only an entry naming the package's own repository on GitHub gets its id.
func TestStampRepoID(t *testing.T) {
	t.Parallel()
	reg := &generate.Registry{Assets: []*generate.Asset{
		{Type: "github_release", RepoOwner: "Cli", RepoName: "CLI"},
		{Type: "github_release", RepoOwner: "other", RepoName: "repo"},
		{Type: "http", URL: "https://example.com/x.tar.gz"},
		{Type: "forgejo_release", Host: "codeberg.org", RepoOwner: "cli", RepoName: "cli"},
	}}
	stampRepoID(reg, repoConfig(212613049))
	got := make([]int64, 0, len(reg.Assets))
	for _, a := range reg.Assets {
		got = append(got, a.RepoID)
	}
	want := []int64{212613049, 0, 0, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d has repo_id %d, want %d", i, got[i], want[i])
		}
	}
}
