package generate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

func names(n ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(n))
	for _, s := range n {
		m[s] = struct{}{}
	}
	return m
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// keylessBundle is a Sigstore bundle of the kind keyless signing produces: a Fulcio
// certificate naming the workflow that asked for it and the issuer that vouched for it.
func keylessBundle(t *testing.T, identity, issuer string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{
			"certificate": map[string]any{"rawBytes": fulcioCertificate(t, identity, issuer)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// certificatePEM is the same certificate as the release publishes it beside a signature,
// which is PEM rather than the bundle's DER.
func certificatePEM(t *testing.T, identity, issuer string) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fulcioCertificate(t, identity, issuer)})
}

// fulcioCertificate is a certificate of the kind Fulcio issues to a workflow: the
// workflow in a URI of the subject, and the issuer in the extension Fulcio records it in.
func fulcioCertificate(t *testing.T, identity, issuer string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	value, err := asn1.Marshal(issuer)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{uri},
		ExtraExtensions: []pkix.Extension{
			{Id: oidIssuerV2, Value: value},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// keyBundle is what signing with a key produces: a hint for the key, and no
// certificate for an identity to be read from.
func keyBundle(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{
			"publicKey":   map[string]any{"hint": "a hint"},
			"tlogEntries": []any{map[string]any{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serving returns a Generator whose downloads are answered from the given bodies,
// keyed by the path of the release asset.
func serving(t *testing.T, bodies map[string][]byte) *Generator {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &toTestServer{host: base.Host, inner: srv.Client().Transport}}
	return New(nil, client)
}

// toTestServer sends a request for github.com to the test server instead.
type toTestServer struct {
	host  string
	inner http.RoundTripper
}

func (t *toTestServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	r.URL.Host = t.host
	return t.inner.RoundTrip(r) //nolint:wrapcheck
}

const (
	bundlePath = "/sigstore/cosign/releases/download/v2.6.1/cosign-darwin-amd64.sigstore.json"
	identity   = "https://github.com/sigstore/cosign/.github/workflows/release.yml@refs/tags/v2.6.1"
	issuer     = "https://token.actions.githubusercontent.com"
)

func input() *Input {
	return &Input{PkgName: "sigstore/cosign", Version: "v2.6.1"}
}

func registryOf(assets ...*aquag2.Asset) *aquag2.Registry {
	return &aquag2.Registry{Assets: assets}
}

// The bundle beside the asset says which workflow signed it, so the entry names that
// workflow rather than allowing any workflow of the repository.
func TestInferSigning(t *testing.T) {
	t.Parallel()
	g := serving(t, map[string][]byte{bundlePath: keylessBundle(t, identity, issuer)})
	reg := registryOf(
		&aquag2.Asset{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64"},
		&aquag2.Asset{OS: "linux", Arch: "amd64", Asset: "cosign-linux-amd64"},
	)
	err := g.inferSigning(context.Background(), discardLogger(), input(), reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json", "cosign-linux-amd64"))
	if err != nil {
		t.Fatal(err)
	}

	got := reg.Assets[0].Cosign
	if got == nil {
		t.Fatal("the asset with a bundle beside it must be signed")
	}
	if *got.Bundle.Asset != "cosign-darwin-amd64.sigstore.json" {
		t.Errorf("the bundle is %q", *got.Bundle.Asset)
	}
	opts := strings.Join(got.Opts, " ")
	if opts != "--certificate-identity "+identity+" --certificate-oidc-issuer "+issuer {
		t.Errorf("the signer isn't the one the certificate names: %v", got.Opts)
	}
	if reg.Assets[1].Cosign != nil {
		t.Errorf("the asset with nothing beside it must not be signed: %+v", reg.Assets[1].Cosign)
	}
}

// smallstep signs its releases from a workflow in smallstep/workflows and publishes a
// signature and a certificate rather than a bundle. The certificate names the signer, so
// the entry is held to it rather than to a workflow of smallstep/certificates -- which is
// what the inference assumes and what no version of this package can verify under.
func TestInferSigning_certificate(t *testing.T) {
	t.Parallel()
	const (
		asset    = "step_darwin_0.28.4_arm64.tar.gz"
		signedBy = "https://github.com/smallstep/workflows/.github/workflows/goreleaser.yml@refs/heads/main"
	)
	download := "/smallstep/certificates/releases/download/v0.28.4/"
	g := serving(t, map[string][]byte{download + asset + ".pem": certificatePEM(t, signedBy, issuer)})
	reg := registryOf(&aquag2.Asset{OS: "darwin", Arch: "arm64", Asset: asset})
	in := &Input{PkgName: "smallstep/certificates", Version: "v0.28.4"}
	if err := g.inferSigning(context.Background(), discardLogger(), in, reg,
		names(asset, asset+".sig", asset+".pem")); err != nil {
		t.Fatal(err)
	}

	got := reg.Assets[0].Cosign
	if got == nil {
		t.Fatal("the asset with a signature beside it must be signed")
	}
	url := "https://github.com/smallstep/certificates/releases/download/{{.Version}}/" + asset
	want := []string{
		"--certificate", url + ".pem",
		"--certificate-identity", signedBy,
		"--certificate-oidc-issuer", issuer,
		"--signature", url + ".sig",
	}
	if diff := cmp.Diff(want, got.Opts); diff != "" {
		t.Error(diff)
	}
}

// smallstep publishes the certificate base64 encoded, which is the other way cosign
// takes one, and the identity reads the same out of it.
func TestParseCertificateSigner_base64(t *testing.T) {
	t.Parallel()
	encoded := base64.StdEncoding.EncodeToString(certificatePEM(t, identity, issuer))
	// Wrapped across lines, as a file holding base64 may be.
	for _, body := range []string{encoded, encoded[:20] + "\n" + encoded[20:] + "\n"} {
		got, err := parseCertificateSigner([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if got.Identity != identity {
			t.Errorf("the identity is %q", got.Identity)
		}
		if got.Issuer != issuer {
			t.Errorf("the issuer is %q", got.Issuer)
		}
	}
}

// What the certificate is read for is the identity. Where cosign reads the signature
// from is the rest of the arguments, and an entry that lost those would verify nothing.
func TestHeldTo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts []string
		want []string
	}{
		{
			name: "a regexp becomes the identity it was a pattern for",
			opts: []string{"--certificate-identity-regexp", "^https://github\\.com/owner/.+$", "--certificate-oidc-issuer", "assumed"},
			want: []string{"--certificate-identity", identity, "--certificate-oidc-issuer", issuer},
		},
		{
			name: "the arguments that aren't about the identity are kept, in place",
			opts: []string{"--certificate", "a.pem", "--certificate-identity-regexp", "a pattern", "--certificate-oidc-issuer", "assumed", "--signature", "a.sig"},
			want: []string{"--certificate", "a.pem", "--certificate-identity", identity, "--certificate-oidc-issuer", issuer, "--signature", "a.sig"},
		},
		{
			name: "an entry saying nothing about the identity is told",
			opts: []string{"--signature", "a.sig"},
			want: []string{"--signature", "a.sig", "--certificate-identity", identity, "--certificate-oidc-issuer", issuer},
		},
		{
			name: "a flag with no value left to read ends it",
			opts: []string{"--certificate-oidc-issuer"},
			want: []string{"--certificate-oidc-issuer", issuer, "--certificate-identity", identity},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, heldTo(tt.opts, &signer{Identity: identity, Issuer: issuer})); diff != "" {
				t.Error(diff)
			}
		})
	}
}

// A bundle signed with a key has no identity to hold a signature to, and the key it
// names doesn't have to be in the release. An entry claiming otherwise can never
// verify, so the release is left to a definition that knows where the key is.
func TestInferSigning_signedWithAKey(t *testing.T) {
	t.Parallel()
	g := serving(t, map[string][]byte{bundlePath: keyBundle(t)})
	reg := registryOf(&aquag2.Asset{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64"})
	if err := g.inferSigning(context.Background(), discardLogger(), input(), reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	if got := reg.Assets[0].Cosign; got != nil {
		t.Errorf("the entry claims a signature it can't be held to: %+v", got.Opts)
	}
}

// Reading the bundle is how the assumption is improved, not how it is made. One that
// can't be read leaves it to the verification that follows.
func TestInferSigning_bundleUnreadable(t *testing.T) {
	t.Parallel()
	g := serving(t, nil)
	reg := registryOf(&aquag2.Asset{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64"})
	if err := g.inferSigning(context.Background(), discardLogger(), input(), reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	got := reg.Assets[0].Cosign
	if got == nil {
		t.Fatal("the configuration was dropped over a bundle that couldn't be read")
	}
	if !strings.Contains(strings.Join(got.Opts, " "), "sigstore/cosign") {
		t.Errorf("the identity isn't held to the repository: %v", got.Opts)
	}
}

// A definition that says how the asset is signed was written by someone who knows the
// release, and it can name a signer nothing in the release does.
func TestInferSigning_definitionWins(t *testing.T) {
	t.Parallel()
	declared := &aquaregistry.Cosign{Opts: []string{"--certificate-identity", "exactly this one"}}
	g := serving(t, nil)
	reg := registryOf(&aquag2.Asset{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64", Cosign: declared})
	if err := g.inferSigning(context.Background(), discardLogger(), input(), reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	if reg.Assets[0].Cosign != declared {
		t.Error("the definition's configuration was replaced")
	}
}
