package run

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aquaproj/ar2/pkg/state"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v93/github"
)

// A server error, a rate limit or a failed connection is the moment, whether downloading an
// asset or asking GitHub's API, and nothing else is.
func TestTransient(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		err  error
		want bool
	}{
		"a download's server error": {err: fmt.Errorf("download: %w", &verify.StatusError{Code: 503}), want: true},
		"the API's server error": {err: fmt.Errorf("get the release: %w", &gogithub.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusBadGateway},
		}), want: true},
		"the API's rate limit":  {err: &gogithub.RateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden}}, want: true},
		"a secondary limit":     {err: &gogithub.AbuseRateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden}}, want: true},
		"the API saying no":     {err: &gogithub.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}, want: false},
		"no asset for anything": {err: errors.New("the release has no asset for any environment"), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := transient(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A version is reported once, on the run that makes it fail the limit in a row, and its count
// starts again once an attempt gets past the moment.
func TestCountFailure(t *testing.T) {
	t.Parallel()
	pkg := &state.Package{}
	bad := &verify.StatusError{Code: http.StatusBadGateway}
	var reported []int
	for run, err := range []error{bad, bad, bad, bad, nil, bad, errors.New("no asset"), bad} {
		if countFailure(pkg, "v1.0.0", err) {
			reported = append(reported, run)
		}
	}
	if len(reported) != 1 || reported[0] != failuresBeforeIssue-1 {
		t.Errorf("reported on runs %v, want only on run %d", reported, failuresBeforeIssue-1)
	}
	if pkg.TransientFailures["v1.0.0"] != 1 {
		t.Errorf("the count is %d, want 1: one failure since the last that wasn't one", pkg.TransientFailures["v1.0.0"])
	}
	countFailure(pkg, "v1.0.0", nil)
	if pkg.TransientFailures != nil {
		t.Errorf("a version that got past is still counted: %v", pkg.TransientFailures)
	}
	if countFailure(nil, "v1.0.0", bad) {
		t.Error("a package with no state was reported")
	}
}
