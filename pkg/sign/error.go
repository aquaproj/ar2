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

var (
	// errNoGH is what a machine with no GitHub CLI gets. Attestations are checked
	// by it, so there is nothing to read the signer out of.
	errNoGH = errors.New("the GitHub CLI isn't installed")
	// errNoAttestation is what an artifact GitHub holds no attestation for gets.
	errNoAttestation = errors.New("the artifact has no attestation")
	// errWrongSource is what an attestation for something built somewhere else
	// gets. It is the check naming the repository to gh would have made.
	errWrongSource = errors.New("the attestation is for another repository")
)

// ErrUnverified says a signature the entry carries couldn't be shown to hold, either
// because it doesn't or because the check couldn't be made.
var ErrUnverified = errors.New("the asset can't be verified the way its entry says it can")
