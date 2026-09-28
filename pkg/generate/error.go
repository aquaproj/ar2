package generate

import "errors"

var (
	errNoPackage     = errors.New("aqua gr returned no package")
	errPkgNameFormat = errors.New("the package name must be <repo_owner>/<repo_name>")
	// errBundleStatus is returned when the bundle beside an asset can't be downloaded.
	errBundleStatus = errors.New("the signature bundle didn't download")
	// errBundleMaterial is returned for a bundle that holds neither a certificate nor a
	// public key, which is neither of the ways cosign verifies one.
	errBundleMaterial = errors.New("the signature bundle says neither a certificate nor a key verifies it")
	// errCertificateNoIdentity is returned for a certificate with no subject to hold a
	// signature to.
	errCertificateNoIdentity = errors.New("the bundle's certificate names no identity")
)
