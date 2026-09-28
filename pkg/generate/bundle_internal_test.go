package generate

import (
	"encoding/json"
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
