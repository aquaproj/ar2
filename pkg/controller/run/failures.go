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

// maxPutBack is the most laps a slow package waits.
const maxPutBack = 32

// putBackGrowth is how much longer a slow package waits each time it is slow again.
const putBackGrowth = 2

// putBack makes the package wait before it is tried again, twice as long as last time.
//
// A server slow enough for a download to run out of time is likely to be next run too, and
// trying it every run would spend a share of each on waiting. It is still a transient
// failure, counted as one, so a package that never gets past it is reported.
func putBack(pkg *state.Package) {
	pkg.PutBack = min(max(1, pkg.PutBack*putBackGrowth), maxPutBack)
	pkg.WaitUntil = pkg.Round + pkg.PutBack
}
