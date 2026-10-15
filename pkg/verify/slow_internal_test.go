package verify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// A download that goes on making progress for longer than one may take is given up on,
// and says so, which is a transient failure rather than one about the release.
func TestVerifier_download_slow(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)
	v := New(srv.Client(), &recordingSignatures{})
	v.downloadLimit = 100 * time.Millisecond
	_, err := v.download(context.Background(), srv.URL+"/spark.tgz", filepath.Join(t.TempDir(), "spark.tgz"))
	if !Slow(err) {
		t.Fatalf("got %v, want a slow download", err)
	}
	if !Transient(err) {
		t.Error("a slow download isn't transient")
	}
}
