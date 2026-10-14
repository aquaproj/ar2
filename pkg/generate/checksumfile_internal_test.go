package generate

import (
	"context"
	"testing"
)

// A checksum file cosign signs is found, held to the signer its certificate names. One
// nothing signs, and one listing a hash no entry records, are left alone.
func TestSignedChecksumFiles(t *testing.T) {
	t.Parallel()
	const signedBy = "https://github.com/foo/foo/.github/workflows/release.yaml@refs/tags/v1.0.0"
	download := "/foo/foo/releases/download/v1.0.0/"
	g := serving(t, map[string][]byte{download + "checksums.txt.pem": certificatePEM(t, signedBy, issuer)})
	files := g.signedChecksumFiles(context.Background(), discardLogger(), &Input{PkgName: "foo/foo", Version: "v1.0.0"}, names(
		"foo_linux_amd64.tar.gz",
		"checksums.txt", "checksums.txt.sig", "checksums.txt.pem",
		"sha512sums.txt", "sha512sums.txt.sig", "sha512sums.txt.pem",
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
