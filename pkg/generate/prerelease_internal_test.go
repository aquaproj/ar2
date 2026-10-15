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

// A version generated from a release marked as a prerelease says so.
func TestGenerate_prerelease(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/tags/v0.21.0-rc.1") {
			_, _ = w.Write([]byte(`{
			  "id": 42, "tag_name": "v0.21.0-rc.1", "prerelease": true,
			  "published_at": "2026-09-26T13:41:29+02:00",
			  "assets": [{"id": 1, "name": "mergiraf_x86_64-unknown-linux-gnu.tar.gz"}]
			}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	generated, err := New(nil, nil).Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
		PkgName: "codeberg.org/mergiraf/mergiraf",
		Version: "v0.21.0-rc.1",
		Base: &aquaregistry.PackageInfo{
			Type:      aquaregistry.PkgInfoTypeForgejoRelease,
			Host:      "codeberg.org",
			RepoOwner: "mergiraf",
			RepoName:  "mergiraf",
		},
		Repos: forge.New(toServer(server), "codeberg.org"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !generated.Registry.Prerelease {
		t.Error("the version isn't marked as a prerelease")
	}
}

// A version of a package that reads no release is a prerelease by how its tag is written.
func TestGenerate_prereleaseTag(t *testing.T) {
	t.Parallel()
	for version, want := range map[string]bool{"v4.3.0-rc2": true, "v4.2.0": false} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			generated, err := New(nil, nil).Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
				PkgName: "apache/spark",
				Version: version,
				Base: &aquaregistry.PackageInfo{
					Type:      aquaregistry.PkgInfoTypeHTTP,
					RepoOwner: "apache",
					RepoName:  "spark",
					URL:       "https://archive.apache.org/dist/spark/spark-{{trimV .Version}}/spark-{{trimV .Version}}-bin-hadoop3.tgz",
					Format:    "tgz",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if generated.Registry.Prerelease != want {
				t.Errorf("prerelease is %t, want %t", generated.Registry.Prerelease, want)
			}
		})
	}
}
