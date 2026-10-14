package run

import (
	"errors"
	"net/http"

	"github.com/aquaproj/ar2/pkg/state"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v92/github"
)

// failuresBeforeIssue is how many runs in a row a version may fail for a reason that should
// pass on another try before it is reported.
//
// One is a bad minute, and the next run is what it is retried by. Three is a version that
// isn't getting past whatever it is, which a person has to look at: an asset a server always
// fails to serve, a rate limit the run always meets at the same point.
const failuresBeforeIssue = 3

// transient reports whether the error is about the moment rather than the release: a server
// error, a rate limit or a connection that failed, whether downloading an asset or asking
// GitHub's API.
func transient(err error) bool {
	if verify.Transient(err) {
		return true
	}
	if _, ok := errors.AsType[*gogithub.RateLimitError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*gogithub.AbuseRateLimitError](err); ok {
		return true
	}
	if resp, ok := errors.AsType[*gogithub.ErrorResponse](err); ok && resp.Response != nil {
		return resp.Response.StatusCode == http.StatusTooManyRequests ||
			resp.Response.StatusCode >= http.StatusInternalServerError
	}
	return false
}

// countFailure records how an attempt at a version went, and reports whether it is the attempt
// that makes the version one that keeps failing.
//
// Only a failure for a reason that should pass counts, and only in a row: an attempt that
// succeeds, or fails for anything else, is a version that got past the moment, and its count
// starts again. It reports once, on the attempt that reaches the limit, so that a version
// failing for weeks is said once rather than on every run.
func countFailure(pkg *state.Package, version string, err error) bool {
	if pkg == nil {
		return false
	}
	if err == nil || !transient(err) {
		delete(pkg.TransientFailures, version)
		if len(pkg.TransientFailures) == 0 {
			pkg.TransientFailures = nil
		}
		return false
	}
	if pkg.TransientFailures == nil {
		pkg.TransientFailures = map[string]int{}
	}
	pkg.TransientFailures[version]++
	return pkg.TransientFailures[version] == failuresBeforeIssue
}
