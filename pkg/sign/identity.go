package sign

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
)

// Identity is who signed an artifact, as cosign is asked to check it.
//
// It is read off the signature rather than guessed at. aqua gr assumes the signer is
// a workflow in the package's own repository, which is right often enough to be
// misleading: sigstore/cosign signs with a service account, and a definition carrying
// that assumption fails for everyone who installs it.
type Identity struct {
	// SAN is the subject alternative name of the signing certificate: a workflow
	// URI for something built by GitHub Actions, an email address for a service
	// account.
	SAN string
	// Issuer is the OIDC issuer that vouched for the SAN. Without it the SAN is
	// only a string anyone could have put in a certificate of their own.
	Issuer string
}

// Opts renders the identity as the cosign flags that check for it.
func (i *Identity) Opts() []string {
	return []string{
		"--certificate-identity", i.SAN,
		"--certificate-oidc-issuer", i.Issuer,
	}
}

// bundle is the part of a Sigstore bundle holding the signing certificate.
//
// A bundle carries the certificate either on its own or as the first entry of a
// chain, depending on which version wrote it.
type bundle struct {
	VerificationMaterial struct {
		Certificate struct {
			RawBytes string `json:"rawBytes"`
		} `json:"certificate"`
		X509CertificateChain struct {
			Certificates []struct {
				RawBytes string `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"x509CertificateChain"`
	} `json:"verificationMaterial"`
}

// identityFromBundle reads who signed out of a .sigstore.json.
func identityFromBundle(b []byte) (*Identity, error) {
	var bdl bundle
	if err := json.Unmarshal(b, &bdl); err != nil {
		return nil, fmt.Errorf("read the bundle as JSON: %w", err)
	}
	raw := bdl.VerificationMaterial.Certificate.RawBytes
	if raw == "" {
		if certs := bdl.VerificationMaterial.X509CertificateChain.Certificates; len(certs) > 0 {
			raw = certs[0].RawBytes
		}
	}
	if raw == "" {
		return nil, errNoCertificate
	}
	der, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode the certificate: %w", err)
	}
	return identityFromDER(der)
}

// identityFromPEM reads who signed out of a certificate file, which is what a
// release carries beside a .sig when it doesn't use a bundle.
func identityFromPEM(b []byte) (*Identity, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errNoCertificate
	}
	return identityFromDER(block.Bytes)
}

// Fulcio records the OIDC issuer in an extension of its own. The first one it used
// holds the issuer as plain bytes; the one that replaced it wraps it in a DER
// UTF8String, and is the one to prefer when both are there.
var (
	oidIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1} //nolint:gochecknoglobals
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8} //nolint:gochecknoglobals
	oidSAN      = asn1.ObjectIdentifier{2, 5, 29, 17}                  //nolint:gochecknoglobals
)

func identityFromDER(der []byte) (*Identity, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse the signing certificate: %w", err)
	}
	san := subjectAlternativeName(cert)
	if san == "" {
		return nil, errNoIdentity
	}
	issuer := issuerOf(cert)
	if issuer == "" {
		return nil, errNoIssuer
	}
	return &Identity{SAN: san, Issuer: issuer}, nil
}

// subjectAlternativeName returns the one name the certificate was issued for.
//
// Which shape it takes says what signed: a URI for a GitHub Actions workflow, an
// email address for a service account. Fulcio also issues certificates whose name is
// an otherName, which Go doesn't parse into a field, so the extension is read
// directly for those.
func subjectAlternativeName(cert *x509.Certificate) string {
	if len(cert.URIs) > 0 {
		return cert.URIs[0].String()
	}
	if len(cert.EmailAddresses) > 0 {
		return cert.EmailAddresses[0]
	}
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0]
	}
	return otherName(cert)
}

// otherName digs the SAN out of the raw extension.
//
// Go leaves an otherName in the extension and out of the parsed fields. The value is
// a UTF8String inside a context-specific tag inside the sequence of names, which is
// enough structure to find it without a full ASN.1 model of the extension.
func otherName(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidSAN) {
			continue
		}
		var names []asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &asn1.RawValue{}); err != nil {
			continue
		}
		var seq asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &seq); err != nil {
			continue
		}
		rest := seq.Bytes
		for len(rest) > 0 {
			var v asn1.RawValue
			r, err := asn1.Unmarshal(rest, &v)
			if err != nil {
				break
			}
			rest = r
			names = append(names, v)
		}
		for _, name := range names {
			if s := utf8String(name.Bytes); s != "" {
				return s
			}
		}
	}
	return ""
}

// utf8String returns the printable value inside an otherName, which holds the OID it
// is for followed by the value.
func utf8String(b []byte) string {
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(b, &oid)
	if err != nil {
		return ""
	}
	var wrapper asn1.RawValue
	if _, err := asn1.Unmarshal(rest, &wrapper); err != nil {
		return ""
	}
	var s string
	if _, err := asn1.Unmarshal(wrapper.Bytes, &s); err != nil {
		return strings.TrimSpace(string(wrapper.Bytes))
	}
	return s
}

// issuerOf returns the OIDC issuer the certificate records.
func issuerOf(cert *x509.Certificate) string {
	var v1 string
	for _, ext := range cert.Extensions {
		switch {
		case ext.Id.Equal(oidIssuerV2):
			var s string
			if _, err := asn1.Unmarshal(ext.Value, &s); err == nil {
				return s
			}
			return strings.TrimSpace(string(ext.Value))
		case ext.Id.Equal(oidIssuerV1):
			v1 = strings.TrimSpace(string(ext.Value))
		}
	}
	return v1
}
