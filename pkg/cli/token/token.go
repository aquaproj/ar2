// Package token builds the GitHub clients ar2 uses beside the ordinary one.
//
// Both are GitHub App installation tokens, and both exist because of something the
// repository's own GITHUB_TOKEN can't do. Neither is required: without them the
// ordinary client is used, which is what a local run does and what works in a
// repository with no rulesets.
package token

import (
	"fmt"
	"os"

	gogithub "github.com/google/go-github/v92/github"
)

// BranchEnv holds the token that creates the package branches.
//
// Creating one has to get past the ruleset requiring status checks, which a brand
// new branch can't have. The app behind it is listed as a bypass actor for that
// ruleset and holds no pull-requests permission, so it can't open or merge a pull
// request and the bypass can't become a way to land an unchecked change.
const BranchEnv = "AR2_BRANCH_TOKEN"

// PREnv holds the token that commits to the head branches and opens the pull
// requests.
//
// A pull request opened or updated with GITHUB_TOKEN gets its workflow runs in an
// approval-required state, so nothing checks it until a person presses a button. A
// registry that opens pull requests on a schedule would need someone for every one
// of them, and auto-merge would never fire. A GitHub App installation token doesn't
// carry that restriction.
//
// The app is not a bypass actor for anything: it opens pull requests and that is
// all, so the checks still decide what merges.
const PREnv = "AR2_PR_TOKEN"

// Client returns a client for the token in env, or nil when it isn't set.
func Client(env string) (*gogithub.Client, error) {
	t := os.Getenv(env)
	if t == "" {
		return nil, nil //nolint:nilnil // not configured is the ordinary case, not a failure
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(t))
	if err != nil {
		return nil, fmt.Errorf("create a GitHub client for %s: %w", env, err)
	}
	return gh, nil
}
