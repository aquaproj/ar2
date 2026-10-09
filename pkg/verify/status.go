package verify

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusError is what a download the server refused comes back as.
//
// Which status it was decides what happens to the entry. A 404 is the release saying it
// has no such asset, which is about the release and will be true next time; a 500 or a
// reset connection is about the moment, and an entry dropped over one would be an
// environment the registry stopped offering because a server hiccupped.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status code %d", e.Code)
}

// Refused reports whether the error is the server saying there is nothing there, rather
// than something that may answer differently in a minute.
//
// The 4xx range, because that is the range about the request: the asset isn't there, or
// isn't ours to have. 429 is the exception -- it is a rate limit, which is the moment
// rather than the asset.
func Refused(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		return false
	}
	if status.Code == http.StatusTooManyRequests {
		return false
	}
	return status.Code >= http.StatusBadRequest && status.Code < http.StatusInternalServerError
}
