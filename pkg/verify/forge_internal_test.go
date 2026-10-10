package verify

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
)

// An entry on a forge instance is downloaded from that instance. An instance serves a
// release asset at the path GitHub does, so what the entry has to say is which instance.
func TestDownloadURL(t *testing.T) {
	t.Parallel()
	data := []struct {
		title string
		asset *generate.Asset
		exp   string
		isErr bool
	}{
		{
			title: "a GitHub release",
			asset: &generate.Asset{Type: "github_release", RepoOwner: "cli", RepoName: "cli", Asset: "gh.tar.gz"},
			exp:   "https://github.com/cli/cli/releases/download/v2.1.0/gh.tar.gz",
		},
		{
			title: "a release on a Forgejo instance",
			asset: &generate.Asset{Type: "forgejo_release", Host: "codeberg.org", RepoOwner: "mergiraf", RepoName: "mergiraf", Asset: "mergiraf.tar.gz"},
			exp:   "https://codeberg.org/mergiraf/mergiraf/releases/download/v2.1.0/mergiraf.tar.gz",
		},
		{
			title: "a release on a Gitea instance",
			asset: &generate.Asset{Type: "gitea_release", Host: "gitea.com", RepoOwner: "gitea", RepoName: "tea", Asset: "tea.xz"},
			exp:   "https://gitea.com/gitea/tea/releases/download/v2.1.0/tea.xz",
		},
		{
			title: "a URL the entry names itself",
			asset: &generate.Asset{Type: "http", URL: "https://example.com/a.tar.gz"},
			exp:   "https://example.com/a.tar.gz",
		},
		{
			title: "nothing to download from",
			asset: &generate.Asset{Type: "github_release", RepoOwner: "cli"},
			isErr: true,
		},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			t.Parallel()
			u, err := downloadURL("v2.1.0", d.asset)
			if d.isErr {
				if err == nil {
					t.Fatal("an error must be returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if u != d.exp {
				t.Fatalf("wanted %s, got %s", d.exp, u)
			}
		})
	}
}
