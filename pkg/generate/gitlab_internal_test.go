package generate

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/forge"
)

// A GitLab project is generated the same way, through a client of its own, and says the
// instance it is on although the definition doesn't: a gitlab_release package is on
// gitlab.com unless it says otherwise, and what reads the entry reads nothing else.
func TestGenerate_gitLab(t *testing.T) { //nolint:funlen,cyclop
	t.Parallel()
	release := `{
	  "tag_name": "v1.122.0",
	  "released_at": "2026-10-09T06:40:31.000Z",
	  "assets": {
	    "links": [
	      {
	        "name": "package: darwin arm64",
	        "url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/a.tar.gz",
	        "direct_asset_url": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0/downloads/glab_1.122.0_darwin_arm64.tar.gz"
	      },
	      {
	        "name": "package: linux amd64",
	        "url": "https://gitlab.com/api/v4/projects/1/packages/generic/glab/1.122.0/b.tar.gz",
	        "direct_asset_url": "https://gitlab.com/gitlab-org/cli/-/releases/v1.122.0/downloads/glab_1.122.0_linux_amd64.tar.gz"
	      }
	    ]
	  }
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/v1.122.0") {
			_, _ = w.Write([]byte(release))
			return
		}
		_, _ = w.Write([]byte(`{"description": "A GitLab CLI tool bringing GitLab to your command line"}`))
	}))
	defer server.Close()

	base := &aquaregistry.PackageInfo{
		Type:          aquaregistry.PkgInfoTypeGitLabRelease,
		RepoOwner:     "gitlab-org",
		RepoName:      "cli",
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin/arm64", "linux/amd64"},
	}
	g := New(nil, nil)
	reg, err := g.Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
		PkgName: "gitlab.com/gitlab-org/cli",
		Version: "v1.122.0",
		Base:    base,
		Repos:   forge.For(toServer(server), base.Type, base.GetHost()),
	})
	if err != nil {
		t.Fatal(err)
	}
	// released_at is what GitLab calls it.
	if reg.PublishedAt == "" {
		t.Fatal("wanted when the release was published")
	}
	want := map[string]string{
		"darwin/arm64": "glab_1.122.0_darwin_arm64.tar.gz",
		"linux/amd64":  "glab_1.122.0_linux_amd64.tar.gz",
	}
	if len(reg.Assets) != len(want) {
		t.Fatalf("wanted an entry per environment the release has, got %d", len(reg.Assets))
	}
	for _, asset := range reg.Assets {
		env := asset.OS + "/" + asset.Arch
		if asset.Type != aquaregistry.PkgInfoTypeGitLabRelease {
			t.Errorf("the entry for %s says %q, want the type the definition says", env, asset.Type)
		}
		if asset.Host != "gitlab.com" {
			t.Errorf("the entry for %s says the host is %q, want the instance the type means", env, asset.Host)
		}
		// The asset is named by the path the permanent link serves it at, not by
		// the label beside it: every one of these links is named "package: ...".
		if w, ok := want[env]; ok && asset.Asset != w {
			t.Errorf("the entry for %s names %q, want %q", env, asset.Asset, w)
		}
		delete(want, env)
	}
	if len(want) != 0 {
		t.Errorf("wanted an entry for every environment the release has, missing %v", want)
	}
}
