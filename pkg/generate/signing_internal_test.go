package generate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
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
	b, err := json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{
			"certificate": map[string]any{"rawBytes": der},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
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
