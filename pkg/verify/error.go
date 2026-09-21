package verify

import "errors"

var errNoDownloadURL = errors.New("the asset has neither a URL nor a repository and an asset name")
