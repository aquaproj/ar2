package github

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// EnableAutoMerge turns on auto-merge for a pull request.
//
// This is a GraphQL mutation because the REST API has no equivalent. Auto-merge is
// what makes CI the gate: the pull request merges itself once the checks pass, and
// stays open when they don't.
//
// It is sent again when GitHub has a bad minute: a 502, 503 or 504, or an answer saying
// something went wrong on its side. A pull request auto-merge didn't reach waits for a
// person while every check on it passes -- alebeck/boring's v0.12.0 was the one of ten
// opened in the same minute that did.
func (c *Client) EnableAutoMerge(ctx context.Context, pullRequestID string) error {
	const mutation = `mutation($id: ID!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) {
    clientMutationId
  }
}`
	vars := map[string]any{"id": pullRequestID}
	wait := c.retryWait
	for attempt := 1; ; attempt++ {
		result := &graphQLResponse{}
		if err := c.post(ctx, mutation, vars, result); err != nil {
			return err
		}
		if len(result.Errors) == 0 {
			return nil
		}
		err := fmt.Errorf("enable auto-merge: %s", result.Errors[0].Message)
		if attempt == queryAttempts || !wentWrong(result.Errors[0].Message) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// wentWrong says whether a GraphQL error is GitHub failing on its side rather than refusing
// the request: "Something went wrong while executing your query. Please try again later."
func wentWrong(message string) bool {
	return strings.HasPrefix(message, "Something went wrong")
}
