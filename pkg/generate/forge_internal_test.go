package generate

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/forge"
)

// A release on a forge instance is generated from the instance's own asset list, which is
// the whole point of the type: the naming is inferred per version rather than written as a
// template that nothing checks.
func TestGenerate_forgeInstance(t *testing.T) { //nolint:funlen,cyclop
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v0.20.0"):
			_, _ = w.Write([]byte(`{
			  "id": 42,
			  "tag_name": "v0.20.0",
			  "published_at": "2026-09-26T13:41:29+02:00",
			  "assets": [
			    {"id": 1, "name": "mergiraf_aarch64-apple-darwin.tar.gz"},
			    {"id": 2, "name": "mergiraf_x86_64-apple-darwin.tar.gz"},
			    {"id": 3, "name": "mergiraf_aarch64-unknown-linux-gnu.tar.gz"},
			    {"id": 4, "name": "mergiraf_x86_64-unknown-linux-gnu.tar.gz"}
			  ]
			}`))
		case strings.HasSuffix(r.URL.Path, "/releases/42/assets"):
			_, _ = w.Write([]byte(`[
			  {"id": 1, "name": "mergiraf_aarch64-apple-darwin.tar.gz"},
			  {"id": 2, "name": "mergiraf_x86_64-apple-darwin.tar.gz"},
			  {"id": 3, "name": "mergiraf_aarch64-unknown-linux-gnu.tar.gz"},
			  {"id": 4, "name": "mergiraf_x86_64-unknown-linux-gnu.tar.gz"}
			]`))
		default:
			_, _ = w.Write([]byte(`{"description": "A syntax-aware git merge driver"}`))
		}
	}))
	defer server.Close()

	g := New(nil, nil)
	reg, err := g.Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
		PkgName: "codeberg.org/mergiraf/mergiraf",
		Version: "v0.20.0",
		Base: &aquaregistry.PackageInfo{
			Type:      aquaregistry.PkgInfoTypeForgejoRelease,
			Host:      "codeberg.org",
			RepoOwner: "mergiraf",
			RepoName:  "mergiraf",
			Replacements: aquaregistry.Replacements{
				"darwin": "apple-darwin",
				"linux":  "unknown-linux-gnu",
				"amd64":  "x86_64",
				"arm64":  "aarch64",
			},
			SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux"},
		},
		Repos: forge.New(toServer(server), "codeberg.org"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Assets) == 0 {
		t.Fatal("wanted an entry per environment, got none")
	}
	// What the release says about itself, which the version string doesn't.
	if reg.PublishedAt == "" {
		t.Fatal("wanted when the release was published")
	}
	for _, asset := range reg.Assets {
		if asset.Type != aquaregistry.PkgInfoTypeForgejoRelease {
			t.Errorf("the entry for %s/%s says %q, want the type the definition says", asset.OS, asset.Arch, asset.Type)
		}
		// Without the instance the entry would name a repository on github.com,
		// where the same owner and name are somebody else's.
		if asset.Host != "codeberg.org" {
			t.Errorf("the entry for %s/%s says the host is %q", asset.OS, asset.Arch, asset.Host)
		}
		if asset.RepoOwner != "mergiraf" || asset.RepoName != "mergiraf" {
			t.Errorf("the entry for %s/%s names %s/%s", asset.OS, asset.Arch, asset.RepoOwner, asset.RepoName)
		}
	}
	// The asset naming is the release's own, read off the list rather than rendered
	// from a template in the definition, which says none.
	want := map[string]string{
		"darwin/arm64": "mergiraf_aarch64-apple-darwin.tar.gz",
		"darwin/amd64": "mergiraf_x86_64-apple-darwin.tar.gz",
		"linux/arm64":  "mergiraf_aarch64-unknown-linux-gnu.tar.gz",
		"linux/amd64":  "mergiraf_x86_64-unknown-linux-gnu.tar.gz",
	}
	for _, asset := range reg.Assets {
		env := asset.OS + "/" + asset.Arch
		if w, ok := want[env]; ok && asset.Asset != w {
			t.Errorf("the entry for %s names %q, want %q", env, asset.Asset, w)
		}
		delete(want, env)
	}
	if len(want) != 0 {
		t.Errorf("wanted an entry for every environment the release has, missing %v", want)
	}
}

// toServer is an HTTP client that answers from the test server whatever instance it is
// asked for, so that the requests the client builds are the ones under test.
func toServer(server *httptest.Server) *http.Client {
	u, err := url.Parse(server.URL)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			to := req.Clone(req.Context())
			to.URL.Scheme = u.Scheme
			to.URL.Host = u.Host
			return server.Client().Transport.RoundTrip(to)
		}),
	}
}

type roundTripper func(req *http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
