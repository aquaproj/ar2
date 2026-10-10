package remove

import "errors"

var (
	// errReasonRequired is returned when no reason was given. The ignored list carries
	// it, and an entry saying only that somebody decided something is the one thing
	// that list must not hold.
	errReasonRequired = errors.New("a reason is required: it is what the registry's configuration records")
	// errNoPackageBranch is returned when the registry doesn't hold the package, so there
	// is nothing to stop serving.
	errNoPackageBranch = errors.New("the registry holds no such package")
	// errPullRequestInFlight is returned when the package has one open.
	errPullRequestInFlight = errors.New("the package has an open pull request, and removing would discard what it holds")
)
