// Package httpclient makes the client ar2 reads what isn't GitHub's API with.
package httpclient

import (
	"log/slog"
	"net/http"

	"github.com/suzuki-shunsuke/go-retryablehttp"
)

// New returns a client that retries what a server failed for a moment, and carries no token.
//
// It is for what isn't GitHub's API: an asset, a signature, a certificate a definition
// names, a forge. The GitHub token is GitHub's, and a host anywhere else has no business
// being handed it, so it starts from a client without one.
//
// A server error, a rate limit and a connection that failed are retried, which is what a
// run would otherwise wait a whole lap to try again. What the server says once the retries
// run out is passed through as the response rather than replaced with an error, because
// the status is what decides whether an asset is missing or the moment was bad: a 404 is
// the release, a 503 is the server.
//
// aqua's own downloader retries on its own, so it is given a client that doesn't:
// see Plain.
func New(logger *slog.Logger) *http.Client {
	return Retryable(logger, Plain())
}

// Retryable wraps client with the retries New describes.
func Retryable(logger *slog.Logger, client *http.Client) *http.Client {
	c := retryablehttp.NewClient()
	c.HTTPClient = client
	c.Logger = logger
	c.ErrorHandler = retryablehttp.PassthroughErrorHandler
	return c.StandardClient()
}

// Plain returns a client that neither retries nor carries a token, for aqua's downloader,
// which adds the retries itself: giving it a client that retries already would retry each
// of its retries.
func Plain() *http.Client {
	return &http.Client{}
}
