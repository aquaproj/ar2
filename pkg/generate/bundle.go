package generate

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"unicode"
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
	b, err := readSmallAsset(ctx, httpClient, url)
	if err != nil {
		return nil, err
	}
	return parseSigner(b)
}

// readCertificateSigner downloads the certificate beside a signature and reads who it names.
//
// A release that signs with a certificate and a signature rather than with a bundle says the
// same thing in another file: the certificate is the one Fulcio issued to whatever asked for
// it, and reading it is what tells the entry who to hold the signature to.
func readCertificateSigner(ctx context.Context, httpClient *http.Client, url string) (*signer, error) {
	b, err := readSmallAsset(ctx, httpClient, url)
	if err != nil {
		return nil, err
	}
	return parseCertificateSigner(b)
}

// readSmallAsset downloads one of the small files beside an asset -- a bundle, a certificate --
// and reads it whole.
func readSmallAsset(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create a request for the signing material: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request the signing material: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errBundleStatus, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, bundleLimit))
	if err != nil {
		return nil, fmt.Errorf("read the signing material: %w", err)
	}
	return b, nil
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
	return signerOf(der)
}

// parseCertificateSigner reads the certificate beside a signature.
func parseCertificateSigner(b []byte) (*signer, error) {
	der, err := certificateDER(b)
	if err != nil {
		return nil, err
	}
	return signerOf(der)
}

// certificateDER is the certificate's own bytes, however the release wrote it down.
//
// cosign's --certificate takes the certificate as PEM or as base64 of it, so a release
// publishes whichever it was given -- smallstep publishes the base64. Either way what
// says who signed is the DER inside.
func certificateDER(b []byte) ([]byte, error) {
	if block, _ := pem.Decode(b); block != nil {
		return block.Bytes, nil
	}
	decoded, err := unbase64(b)
	if err != nil {
		return nil, errCertificateEncoding
	}
	if block, _ := pem.Decode(decoded); block != nil {
		return block.Bytes, nil
	}
	return decoded, nil
}

// unbase64 decodes base64 that may have been wrapped across lines.
func unbase64(b []byte) ([]byte, error) {
	packed := bytes.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, b)
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(packed)))
	n, err := base64.StdEncoding.Decode(decoded, packed)
	if err != nil {
		return nil, fmt.Errorf("decode the certificate as base64: %w", err)
	}
	return decoded[:n], nil
}

// signerOf reads who a certificate names.
func signerOf(der []byte) (*signer, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("read the certificate: %w", err)
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
