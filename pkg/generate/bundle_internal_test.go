package generate

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"
)

// An older bundle carries the certificate as a chain rather than on its own, and the
// signer is the first certificate in it.
func TestParseSigner_certificateChain(t *testing.T) {
	t.Parallel()
	keyless := keylessBundle(t, identity, issuer)
	raw := map[string]any{}
	if err := json.Unmarshal(keyless, &raw); err != nil {
		t.Fatal(err)
	}
	material, ok := raw["verificationMaterial"].(map[string]any)
	if !ok {
		t.Fatal("the bundle holds no verification material")
	}
	cert, ok := material["certificate"].(map[string]any)
	if !ok {
		t.Fatal("the bundle holds no certificate")
	}
	delete(material, "certificate")
	material["x509CertificateChain"] = map[string]any{"certificates": []any{cert}}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := parseSigner(b)
	if err != nil {
		t.Fatal(err)
	}
	if signer.Identity != identity {
		t.Errorf("the identity is %q", signer.Identity)
	}
	if signer.Issuer != issuer {
		t.Errorf("the issuer is %q", signer.Issuer)
	}
}

// A bundle verified by neither a certificate nor a key is neither of the ways cosign
// checks one, and saying nothing about it beats guessing.
func TestParseSigner_neither(t *testing.T) {
	t.Parallel()
	_, err := parseSigner([]byte(`{"verificationMaterial":{}}`))
	if !errors.Is(err, errBundleMaterial) {
		t.Fatalf("the error is %v", err)
	}
}

// Not a bundle at all.
func TestParseSigner_notJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseSigner([]byte("<html>")); err == nil {
		t.Fatal("an error should be returned")
	}
}

// A certificate that isn't one. Anything can be beside an asset under a name ending in
// .pem, and what it says about the signer is nothing.
func TestParseCertificateSigner_notACertificate(t *testing.T) {
	t.Parallel()
	_, err := parseCertificateSigner([]byte("<html>"))
	if !errors.Is(err, errCertificateEncoding) {
		t.Fatalf("the error is %v", err)
	}
}

// cosign's sign-blob --bundle used to write the certificate as base64 of its PEM, beside
// the signature: ouch-org/ouch's 0.8.3 bundles.
func TestParseSigner_legacyBundle(t *testing.T) {
	t.Parallel()
	raw := &rawBundle{}
	if err := json.Unmarshal(keylessBundle(t, identity, issuer), raw); err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw.VerificationMaterial.Certificate.RawBytes})
	b, err := json.Marshal(map[string]string{
		"base64Signature": "c2lnbmF0dXJl",
		"cert":            base64.StdEncoding.EncodeToString(certPEM),
	})
	if err != nil {
		t.Fatal(err)
	}

	signer, err := parseSigner(b)
	if err != nil {
		t.Fatal(err)
	}
	if signer.Identity != identity {
		t.Errorf("the identity is %q", signer.Identity)
	}
	if signer.Issuer != issuer {
		t.Errorf("the issuer is %q", signer.Issuer)
	}
}
