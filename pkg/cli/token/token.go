// Package token builds the GitHub clients ar2 uses beside the ordinary one.
//
// Both are GitHub App installation tokens, and both exist because of something the
// repository's own GITHUB_TOKEN can't do. Neither is required: without them the
// ordinary client is used, which is what a local run does and what works in a
// repository with no rulesets.
package token

import (
	"context"
	"fmt"
	"net/http"
	"os"

	gogithub "github.com/google/go-github/v92/github"
	"golang.org/x/oauth2"
)

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

// HTTPClient returns an HTTP client carrying the token in env, or nil when it isn't set.
//
// It is for the parts of GitHub ar2 reaches through GraphQL, which take a client rather
// than a token.
func HTTPClient(ctx context.Context, env string) *http.Client {
	t := os.Getenv(env)
	if t == "" {
		return nil
	}
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: t}))
}

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
