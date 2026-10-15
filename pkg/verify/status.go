package verify

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
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

// Transient reports whether the error is about the moment rather than the release: a server
// error, a rate limit, or a connection that failed. The same download may well work on the
// next run, so what it says about the release is nothing yet.
func Transient(err error) bool {
	if Slow(err) {
		return true
	}
	if status, ok := errors.AsType[*StatusError](err); ok {
		return status.Code == http.StatusTooManyRequests || status.Code >= http.StatusInternalServerError
	}
	if _, ok := errors.AsType[*url.Error](err); ok {
		return true
	}
	_, ok := errors.AsType[net.Error](err)
	return ok
}

// SlowError is a download that didn't finish within the time one may take.
type SlowError struct {
	URL   string
	Limit time.Duration
}

func (e *SlowError) Error() string {
	return fmt.Sprintf("the download took longer than %s: %s", e.Limit, e.URL)
}

// Slow reports whether the error is a download that took too long. It is transient --
// the server may be faster next time -- and it is also what a package is put back for,
// so that a server that is always this slow doesn't hold up every run.
func Slow(err error) bool {
	_, ok := errors.AsType[*SlowError](err)
	return ok
}
