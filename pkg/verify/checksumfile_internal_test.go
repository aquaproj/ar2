package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/google/go-cmp/cmp"
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
		{OS: "linux", Arch: "amd64", Type: "github_release", RepoOwner: "foo", RepoName: "foo", Asset: "foo_linux_amd64.tar.gz", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{OS: "darwin", Arch: "arm64", Type: "github_release", RepoOwner: "foo", RepoName: "foo", Asset: "foo_darwin_arm64.tar.gz", Checksum: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"},
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
			content:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  foo_linux_amd64.tar.gz\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  foo_darwin_arm64.tar.gz\ncccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  foo_windows_amd64.zip\n",
			signatures: &recordingSignatures{},
			check:      func(err error) bool { return err == nil },
			want:       "no error",
		},
		{
			name:       "an entry disagrees",
			status:     http.StatusOK,
			content:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  foo_linux_amd64.tar.gz\ndddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  foo_darwin_arm64.tar.gz\n",
			signatures: &recordingSignatures{},
			check:      Tampered,
			want:       "tampering",
		},
		{
			// The file is read only once its signature holds.
			name:       "the signature doesn't verify",
			status:     http.StatusOK,
			content:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  foo_linux_amd64.tar.gz\n",
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

// caddy's checksums.txt lists SHA512, which its name doesn't say. The digest's length does,
// and the asset is held to what it hashed to under that algorithm when it was downloaded
// rather than to its SHA256.
func TestCheckChecksumFiles_sha512(t *testing.T) {
	t.Parallel()
	files := []*generate.ChecksumFile{{Asset: "checksums.txt", Algorithm: "sha256"}}
	sha512Of := func(c string) string { return strings.Repeat(c, 128) }
	tests := []struct {
		name    string
		content string
		check   func(error) bool
		want    string
	}{
		{
			name:    "the asset agrees",
			content: sha512Of("e") + "  foo_linux_amd64.tar.gz\n",
			check:   func(err error) bool { return err == nil },
			want:    "no error",
		},
		{
			name:    "the asset disagrees",
			content: sha512Of("f") + "  foo_linux_amd64.tar.gz\n",
			check:   Tampered,
			want:    "tampering",
		},
		{
			// The darwin asset wasn't downloaded, so nothing here knows its SHA512 and the
			// file says nothing checkable about it.
			name:    "an asset that wasn't downloaded",
			content: sha512Of("f") + "  foo_darwin_arm64.tar.gz\n",
			check:   func(err error) bool { return err == nil },
			want:    "no error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := New(checksumServer(t, http.StatusOK, tt.content), &recordingSignatures{})
			reg := checksumRegistry()
			url, err := downloadURL("v1.0.0", reg.Assets[0])
			if err != nil {
				t.Fatal(err)
			}
			v.digests.record(url, map[string]string{algoSHA512: sha512Of("e")})
			err = v.CheckChecksumFiles(context.Background(), discardLogger(), "foo/foo", "v1.0.0", reg, files)
			if !tt.check(err) {
				t.Errorf("got %v, want %s", err, tt.want)
			}
		})
	}
}

func TestAlgorithmOf(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{32: "md5", 40: "sha1", 64: "sha256", 128: "sha512", 4: "", 96: ""} {
		if got := algorithmOf(strings.Repeat("a", n)); got != want {
			t.Errorf("a digest of %d digits is %q, want %q", n, got, want)
		}
	}
}

// The digests are computed while the asset streams past, one per algorithm.
func TestHasher(t *testing.T) {
	t.Parallel()
	h := newHasher()
	if _, err := io.WriteString(h.writer(), "abc"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"md5":    "900150983cd24fb0d6963f7d28e17f72",
		"sha1":   "a9993e364706816aba3e25717850c26c9cd0d89d",
		"sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"sha512": "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
	}
	if diff := cmp.Diff(want, h.sums()); diff != "" {
		t.Error(diff)
	}
}

// A template's asset, downloaded already, is held to what the checksum file its definition
// names lists for it.
func TestCheckListedChecksum(t *testing.T) {
	t.Parallel()
	const assetURL = "https://example.com/v1.0.0/foo_linux_amd64.tar.gz"
	sha := strings.Repeat("a", 64)
	asset := &generate.Asset{OS: "linux", Arch: "amd64", Type: "http", URL: assetURL}
	cfg := &aquaregistry.Checksum{Type: "http", URL: "https://example.com/v1.0.0/checksums.txt", Algorithm: "sha256"}
	for name, tc := range map[string]struct {
		content string
		err     error
		ok      bool
	}{
		"listed":     {content: sha + "  foo_linux_amd64.tar.gz\n", ok: true},
		"tampered":   {content: strings.Repeat("b", 64) + "  foo_linux_amd64.tar.gz\n", err: ErrTampered},
		"not listed": {content: sha + "  foo_darwin_arm64.tar.gz\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := New(checksumServer(t, http.StatusOK, tc.content), nil)
			v.digests.record(assetURL, map[string]string{"sha256": sha})
			err := v.CheckListedChecksum(t.Context(), slog.New(slog.DiscardHandler), "v1.0.0", asset, cfg.URL, "foo_linux_amd64.tar.gz", cfg)
			switch {
			case tc.ok && err != nil:
				t.Errorf("refused: %v", err)
			case !tc.ok && err == nil:
				t.Error("must be refused")
			case tc.err != nil && !errors.Is(err, tc.err):
				t.Errorf("got %v, want %v", err, tc.err)
			}
		})
	}
}
