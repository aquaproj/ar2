package sign

import "errors"

var (
	// errNoCertificate is what a signature with nothing to read a signer from gets.
	errNoCertificate = errors.New("the signature carries no certificate")
	// errNoIdentity is what a certificate with no subject alternative name gets.
	errNoIdentity = errors.New("the signing certificate names no identity")
	// errNoIssuer is what a certificate with no OIDC issuer gets. The identity
	// alone is a string anyone could put in a certificate of their own.
	errNoIssuer = errors.New("the signing certificate names no OIDC issuer")
)
