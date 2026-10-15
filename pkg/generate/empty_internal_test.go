package generate

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/forge"
)

// A release that holds no asset at all says so, with when it was published, rather than
// being a release whose assets name no environment: a definition can settle that one, and
// nothing settles this one.
func TestGenerate_emptyRelease(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/tags/v13.0.0") {
			_, _ = w.Write([]byte(`{"id": 42, "tag_name": "v13.0.0", "published_at": "2025-11-25T13:55:04Z", "assets": []}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	_, err := New(nil, nil).Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
		PkgName: "codeberg.org/XAMPPRocky/tokei",
		Version: "v13.0.0",
		Base: &aquaregistry.PackageInfo{
			Type:      aquaregistry.PkgInfoTypeForgejoRelease,
			Host:      "codeberg.org",
			RepoOwner: "XAMPPRocky",
			RepoName:  "tokei",
		},
		Repos: forge.New(toServer(server), "codeberg.org"),
	})
	e, ok := errors.AsType[*EmptyReleaseError](err)
	if !ok {
		t.Fatalf("got %v, want an empty release", err)
	}
	if want := time.Date(2025, 11, 25, 13, 55, 4, 0, time.UTC); !e.PublishedAt.Equal(want) {
		t.Errorf("published at %v, want %v", e.PublishedAt, want)
	}
}
