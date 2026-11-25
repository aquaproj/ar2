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

func TestUnsettled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 25, 12, 41, 0, 0, time.UTC)
	tests := []struct {
		name       string
		lastUpload time.Time
		settle     time.Duration
		wantErr    bool
	}{
		{
			name:       "an asset uploaded six minutes ago",
			lastUpload: now.Add(-6 * time.Minute),
			settle:     time.Hour,
			wantErr:    true,
		},
		{
			name:       "assets that have stayed as they are for the hour",
			lastUpload: now.Add(-time.Hour),
			settle:     time.Hour,
		},
		{
			name:       "a run that doesn't wait",
			lastUpload: now.Add(-time.Minute),
		},
		{
			name:   "a release whose assets say nothing about when",
			settle: time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := unsettled(&release{lastUpload: tt.lastUpload}, tt.settle, now)
			_, ok := errors.AsType[*UnsettledReleaseError](err)
			if ok != tt.wantErr {
				t.Errorf("got %v, want an unsettled release: %t", err, tt.wantErr)
			}
		})
	}
}

// The newest asset decides, and an asset uploaded again counts from when it was replaced:
// cloudnative-pg's v1.31.0-rc1 had every asset uploaded again more than an hour after it was
// published.
func TestGenerate_unsettledRelease(t *testing.T) {
	t.Parallel()
	lastUpload := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	body := `{"id": 42, "tag_name": "v1.0.0", "published_at": "2025-11-25T11:33:44Z", "assets": [` +
		`{"id": 1, "name": "foo_linux_amd64.tar.gz", "created_at": "2025-11-25T11:34:00Z"},` +
		`{"id": 2, "name": "foo_darwin_arm64.tar.gz", "created_at": "` + lastUpload.Format(time.RFC3339) + `"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0") {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	_, err := New(nil, nil).Generate(context.Background(), slog.New(slog.DiscardHandler), &Input{
		PkgName: "codeberg.org/foo/foo",
		Version: "v1.0.0",
		Base: &aquaregistry.PackageInfo{
			Type:      aquaregistry.PkgInfoTypeForgejoRelease,
			Host:      "codeberg.org",
			RepoOwner: "foo",
			RepoName:  "foo",
		},
		Repos:  forge.New(toServer(server), "codeberg.org"),
		Settle: time.Hour,
	})
	e, ok := errors.AsType[*UnsettledReleaseError](err)
	if !ok {
		t.Fatalf("got %v, want an unsettled release", err)
	}
	if !e.LastUpload.Equal(lastUpload) {
		t.Errorf("last upload %v, want %v", e.LastUpload, lastUpload)
	}
}
