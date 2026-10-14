package verify

import (
	"errors"

	"github.com/aquaproj/ar2/pkg/sign"
)

var errNoDownloadURL = errors.New("the asset has neither a URL nor a repository and an asset name")

// ErrTampered is an asset that isn't what the release says it is: the digest GitHub
// reports for it disagrees with the bytes downloaded. A signature that doesn't verify is
// the other way to find that out, and Tampered says either.
var ErrTampered = errors.New("the asset isn't what the release says it is")

// Tampered reports whether the error says the release can't be trusted as it stands: an
// asset whose digest disagrees with its bytes, or a signature that doesn't verify.
//
// Such a version is stopped rather than offered without the environment. The release is
// what is in doubt, not one of its assets, so the environments that did check out are no
// more trustworthy than the one that didn't.
func Tampered(err error) bool {
	return errors.Is(err, ErrTampered) || errors.Is(err, sign.ErrUnverified)
}
