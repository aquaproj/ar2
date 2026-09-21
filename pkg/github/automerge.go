package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// EnableAutoMerge turns on auto-merge for a pull request.
//
// This is a GraphQL mutation because the REST API has no equivalent. Auto-merge is
// what makes CI the gate: the pull request merges itself once the checks pass, and
// stays open when they don't.
func (c *Client) EnableAutoMerge(ctx context.Context, pullRequestID string) error {
	const mutation = `mutation($id: ID!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) {
    clientMutationId
  }
}`
	body, err := json.Marshal(map[string]any{
		"query":     mutation,
		"variables": map[string]any{"id": pullRequestID},
	})
	if err != nil {
		return fmt.Errorf("marshal a GraphQL request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create a GraphQL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send a GraphQL request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("send a GraphQL request: status code %d", resp.StatusCode)
	}
	result := &graphQLResponse{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("read a GraphQL response as JSON: %w", err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("enable auto-merge: %s", result.Errors[0].Message)
	}
	return nil
}
