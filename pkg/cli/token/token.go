// Package token finds the token of the ordinary GitHub client and builds the clients
// ar2 uses beside it.
//
// The ones beside it are GitHub App installation tokens, and both exist because of something the
// repository's own GITHUB_TOKEN can't do. Neither is required: without them the
// ordinary client is used, which is what a local run does and what works in a
// repository with no rulesets.
package token

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	gogithub "github.com/google/go-github/v93/github"
	"github.com/suzuki-shunsuke/ghtkn-go-sdk/ghtkn"
	"golang.org/x/oauth2"
)

// GitHub returns the token source the ordinary client uses, or nil when there is none.
//
// GITHUB_TOKEN wins. Without it the token comes from ghtkn when ghtkn is set up, so
// a local run needs no token in the environment. ghtkn is only asked for a token when
// a request needs one, and it never starts the device flow: a token that has to be
// authenticated again is an error naming 'ghtkn auth'.
func GitHub(logger *slog.Logger) (oauth2.TokenSource, error) {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: t}), nil
	}
	enabled, err := ghtkn.Enabled(nil)
	if err != nil {
		return nil, fmt.Errorf("check if ghtkn is enabled: %w", err)
	}
	if !enabled {
		return nil, nil //nolint:nilnil // no token is what the caller decides about
	}
	client, err := ghtkn.New()
	if err != nil {
		return nil, fmt.Errorf("create a ghtkn client: %w", err)
	}
	return client.TokenSource(logger, &ghtkn.InputGet{}), nil
}

// APIEnv holds the token GitHub's API is asked with, when it isn't GITHUB_TOKEN.
//
// GITHUB_TOKEN is what the state is read and written with, in the container registry, which
// a GitHub App installation token can't reach. As the token of the API it is the repository's
// Actions token, which allows a thousand requests an hour, shared with every workflow of the
// repository: a chain of runs used it up, and the next run failed before it started, while
// installing ar2. An installation token of the registry's app has a budget of its own, five
// times that at least.
const APIEnv = "AR2_API_TOKEN"

// API returns the token source the client of GitHub's API uses: APIEnv's token when it is
// set, and otherwise what GitHub returns.
func API(logger *slog.Logger) (oauth2.TokenSource, error) {
	if t := os.Getenv(APIEnv); t != "" {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: t}), nil
	}
	return GitHub(logger)
}

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
