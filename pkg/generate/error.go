package generate

import (
	"errors"
	"time"
)

var (
	// errTie is returned when nothing in a release's asset names tells apart the assets of
	// an environment. A filter in the definition is what settles it.
	errTie = errors.New("assets of an environment can't be told apart by their names")
	// errNoAssetForEnv is why an environment the definition's supported_envs names is left
	// out when nothing was picked for it.
	errNoAssetForEnv = errors.New("the release has no asset for an environment the definition supports")
	// errNoAsset is returned when a release holds no asset for any environment.
	errNoAsset       = errors.New("the release has no asset for any environment")
	errPkgNameFormat = errors.New("the package name must be <repo_owner>/<repo_name>")
	// errBundleStatus is returned when the bundle beside an asset can't be downloaded.
	errBundleStatus = errors.New("the signature bundle didn't download")
	// errBundleMaterial is returned for a bundle that holds neither a certificate nor a
	// public key, which is neither of the ways cosign verifies one.
	errBundleMaterial = errors.New("the signature bundle says neither a certificate nor a key verifies it")
	// errCertificateEncoding is returned when the file beside a signature isn't a
	// certificate written either of the ways cosign reads one.
	errCertificateEncoding = errors.New("the certificate beside the signature is neither PEM nor base64")
	// errCertificateNoIdentity is returned for a certificate with no subject to hold a
	// signature to.
	errCertificateNoIdentity = errors.New("the certificate names no identity")
)

// EmptyReleaseError says the release holds no asset at all: nothing that is uploaded, let
// alone one for an environment. tokei stopped publishing builds at v13.0.0-alpha.8 and
// went on making releases.
//
// It is told apart from a release whose assets name no environment, which a definition
// can settle with asset_rules. Nothing settles this one, unless assets are still being
// uploaded, which is why it carries when the release was published.
type EmptyReleaseError struct {
	// PublishedAt is when the release was published. Zero for one never published.
	PublishedAt time.Time
}

func (e *EmptyReleaseError) Error() string {
	return "the release has no assets"
}
