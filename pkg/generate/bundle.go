package generate

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// signer is how a Sigstore bundle was signed, read from the bundle itself.
//
// A bundle says which of the two kinds it is. Keyless signing puts a Fulcio
// certificate in it, and the certificate names the workflow that asked for it and the
// issuer that vouched for that workflow -- which is what a signature has to be held
// to for checking it to mean anything. Signing with a key puts a hint for the key
// instead and no certificate at all, and then there is no identity to hold anything
// to: what verifies it is the key, which the release doesn't have to carry.
type signer struct {
	// Identity is what the certificate names as its subject, and is empty when the
	// bundle carries a key rather than a certificate.
	Identity string
	// Issuer is who vouched for the identity, empty when the certificate doesn't say.
	Issuer string
}

// bundleLimit is how much of a bundle is read. One holds a signature, a certificate
// and a transparency log entry, and comes to a few kilobytes; a release asset far
// larger than this is not one, whatever it is named.
const bundleLimit = 1 << 20

// readSigner downloads a Sigstore bundle and reads how it was signed.
func readSigner(ctx context.Context, httpClient *http.Client, url string) (*signer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create a request for the bundle: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request the bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errBundleStatus, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, bundleLimit))
	if err != nil {
		return nil, fmt.Errorf("read the bundle: %w", err)
	}
	return parseSigner(b)
}

// rawBundle is the part of the Sigstore bundle format that says who signed.
type rawBundle struct {
	VerificationMaterial struct {
		Certificate *struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificate"`
		X509CertificateChain *struct {
			Certificates []struct {
				RawBytes []byte `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"x509CertificateChain"`
		PublicKey *struct {
			Hint string `json:"hint"`
		} `json:"publicKey"`
	} `json:"verificationMaterial"`
}

// parseSigner reads the bundle's verification material.
func parseSigner(b []byte) (*signer, error) {
	raw := &rawBundle{}
	if err := json.Unmarshal(b, raw); err != nil {
		return nil, fmt.Errorf("read the bundle as JSON: %w", err)
	}
	material := raw.VerificationMaterial
	var der []byte
	switch {
	case material.Certificate != nil:
		der = material.Certificate.RawBytes
	case material.X509CertificateChain != nil && len(material.X509CertificateChain.Certificates) > 0:
		der = material.X509CertificateChain.Certificates[0].RawBytes
	case material.PublicKey != nil:
		// Signed with a key. There is nothing here to constrain but the key.
		return &signer{}, nil
	default:
		return nil, errBundleMaterial
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("read the bundle's certificate: %w", err)
	}
	if len(cert.URIs) == 0 {
		return nil, errCertificateNoIdentity
	}
	return &signer{Identity: cert.URIs[0].String(), Issuer: certificateIssuer(cert)}, nil
}

// The extensions a Fulcio certificate records the issuer in. The newer one holds a
// DER string, the older one the value as it is.
// https://github.com/sigstore/fulcio/blob/main/docs/oid-info.md
var (
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8} //nolint:gochecknoglobals
	oidIssuer   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1} //nolint:gochecknoglobals
)

// certificateIssuer is who vouched for the certificate's identity, or empty when the
// certificate doesn't say.
func certificateIssuer(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidIssuerV2) {
			continue
		}
		var s string
		if _, err := asn1.Unmarshal(ext.Value, &s); err == nil {
			return s
		}
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidIssuer) {
			return string(ext.Value)
		}
	}
	return ""
}
