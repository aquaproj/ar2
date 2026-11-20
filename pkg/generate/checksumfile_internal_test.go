package generate

import (
	"context"
	"testing"
)

// A checksum file cosign signs is found, held to the signer its certificate names, One nothing
// signs is left alone.
func TestSignedChecksumFiles(t *testing.T) {
	t.Parallel()
	const signedBy = "https://github.com/foo/foo/.github/workflows/release.yaml@refs/tags/v1.0.0"
	download := "/foo/foo/releases/download/v1.0.0/"
	g := serving(t, map[string][]byte{download + "checksums.txt.pem": certificatePEM(t, signedBy, issuer)})
	files := g.signedChecksumFiles(context.Background(), discardLogger(), &Input{PkgName: "foo/foo", Version: "v1.0.0"}, names(
		"foo_linux_amd64.tar.gz",
		"checksums.txt", "checksums.txt.sig", "checksums.txt.pem",
		"other_checksums.txt",
	))
	if len(files) != 1 {
		t.Fatalf("found %d signed checksum files: %+v", len(files), files)
	}
	f := files[0]
	if f.Asset != "checksums.txt" || f.Algorithm != "sha256" {
		t.Errorf("the checksum file is %+v", f)
	}
	if f.Cosign == nil || len(f.Cosign.Opts) == 0 {
		t.Fatalf("how it is signed is %+v", f.Cosign)
	}
	found := false
	for _, opt := range f.Cosign.Opts {
		if opt == signedBy {
			found = true
		}
	}
	if !found {
		t.Errorf("the signer isn't the one the certificate names: %v", f.Cosign.Opts)
	}
}

// A checksum file listing a hash other than SHA256 is read too: what is compared goes by
// each digest's length, and every algorithm a file may list is computed as the asset is
// downloaded.
func TestSignedChecksumFiles_sha512(t *testing.T) {
	t.Parallel()
	const signedBy = "https://github.com/foo/foo/.github/workflows/release.yaml@refs/tags/v1.0.0"
	download := "/foo/foo/releases/download/v1.0.0/"
	g := serving(t, map[string][]byte{download + "sha512sums.txt.pem": certificatePEM(t, signedBy, issuer)})
	files := g.signedChecksumFiles(context.Background(), discardLogger(), &Input{PkgName: "foo/foo", Version: "v1.0.0"}, names(
		"foo_linux_amd64.tar.gz", "sha512sums.txt", "sha512sums.txt.sig", "sha512sums.txt.pem",
	))
	if len(files) != 1 || files[0].Asset != "sha512sums.txt" || files[0].Algorithm != "sha512" {
		t.Fatalf("the signed checksum files are %+v", files)
	}
}
