package verify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
)

// toServer sends every request to the test server, whatever host it names: the checksum file
// is downloaded from github.com's release URL.
type toServer struct {
	server *httptest.Server
}

func (t *toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.server.URL)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return t.server.Client().Transport.RoundTrip(r) //nolint:wrapcheck
}

// failingSignatures says every signature doesn't verify.
type failingSignatures struct{}

func (failingSignatures) Check(_ context.Context, _ *slog.Logger, _, _ string, _ *generate.Asset, _ string) error {
	return fmt.Errorf("%w: cosign: no matching signatures", sign.ErrUnverified)
}

func checksumRegistry() *generate.Registry {
	return &generate.Registry{Assets: []*generate.Asset{
		{OS: "linux", Arch: "amd64", Type: "github_release", RepoOwner: "foo", RepoName: "foo", Asset: "foo_linux_amd64.tar.gz", Checksum: "aaaa"},
		{OS: "darwin", Arch: "arm64", Type: "github_release", RepoOwner: "foo", RepoName: "foo", Asset: "foo_darwin_arm64.tar.gz", Checksum: "BBBB"},
	}}
}

func checksumServer(t *testing.T, status int, content string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, content)
	}))
	t.Cleanup(srv.Close)
	return &http.Client{Transport: &toServer{server: srv}}
}

// The entries are held to the digests the signed checksum file lists for their assets.
func TestCheckChecksumFiles(t *testing.T) {
	t.Parallel()
	files := []*generate.ChecksumFile{{Asset: "checksums.txt", Algorithm: "sha256"}}
	tests := []struct {
		name       string
		status     int
		content    string
		signatures Signatures
		check      func(error) bool
		want       string
	}{
		{
			// A digest written in another case is the same digest, and an asset the file
			// doesn't list is nothing it says anything about.
			name:       "the entries agree",
			status:     http.StatusOK,
			content:    "aaaa  foo_linux_amd64.tar.gz\nbbbb  foo_darwin_arm64.tar.gz\ncccc  foo_windows_amd64.zip\n",
			signatures: &recordingSignatures{},
			check:      func(err error) bool { return err == nil },
			want:       "no error",
		},
		{
			name:       "an entry disagrees",
			status:     http.StatusOK,
			content:    "aaaa  foo_linux_amd64.tar.gz\ndddd  foo_darwin_arm64.tar.gz\n",
			signatures: &recordingSignatures{},
			check:      Tampered,
			want:       "tampering",
		},
		{
			// The file is read only once its signature holds.
			name:       "the signature doesn't verify",
			status:     http.StatusOK,
			content:    "aaaa  foo_linux_amd64.tar.gz\n",
			signatures: failingSignatures{},
			check:      Tampered,
			want:       "tampering",
		},
		{
			name:       "a server error",
			status:     http.StatusBadGateway,
			signatures: &recordingSignatures{},
			check:      Transient,
			want:       "an error the next run tries again",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := New(checksumServer(t, tt.status, tt.content), tt.signatures)
			err := v.CheckChecksumFiles(context.Background(), discardLogger(), "foo/foo", "v1.0.0", checksumRegistry(), files)
			if !tt.check(err) {
				t.Errorf("got %v, want %s", err, tt.want)
			}
		})
	}
}

// Without a signed checksum file there is nothing to check, and nothing is downloaded.
func TestCheckChecksumFiles_nothingToCheck(t *testing.T) {
	t.Parallel()
	v := New(checksumServer(t, http.StatusInternalServerError, ""), &recordingSignatures{})
	if err := v.CheckChecksumFiles(context.Background(), discardLogger(), "foo/foo", "v1.0.0", checksumRegistry(), nil); err != nil {
		t.Fatal(err)
	}
}
