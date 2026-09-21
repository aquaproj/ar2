package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// reasonForbidden is what GitHub answers when an organization's IP allow list
// refuses the request.
//
// The allow list covers public repositories too, as long as the request is
// authenticated, and the GitHub Actions token counts. So a package owned by such an
// organization has no star count when ar2 runs in Actions, while the same query
// succeeds from a machine inside the allowed range.
const reasonForbidden = "FORBIDDEN"

// anonymousEndpoint is the REST API, which is where an unauthenticated request can
// still read a public repository. GraphQL has no anonymous access at all.
const anonymousEndpoint = "https://api.github.com/repos/"

// FillForbiddenStars looks up, without a token, the repositories an organization's
// IP allow list refused.
//
// Anonymous requests are not covered by an allow list, so this recovers what the
// authenticated query couldn't read. It is deliberately narrow: anonymous requests
// are limited to 60 an hour, and only the refused repositories are retried. A
// repository that genuinely doesn't exist is left alone, since asking again would
// only spend that budget.
func (c *Client) FillForbiddenStars(ctx context.Context, stars map[string]int, reasons map[string]string) {
	for repo, reason := range reasons {
		if reason != reasonForbidden {
			continue
		}
		star, err := anonymousStars(ctx, repo)
		if err != nil {
			continue
		}
		stars[repo] = star
		delete(reasons, repo)
	}
}

// anonymousStars reads a public repository's star count without a token.
func anonymousStars(ctx context.Context, repo string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anonymousEndpoint+repo, nil)
	if err != nil {
		return 0, fmt.Errorf("create a request for the repository: %w", err)
	}
	// No Authorization header: adding one is what the allow list rejects.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get the repository: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("get the repository: status code %d", resp.StatusCode)
	}
	var body struct {
		StargazersCount int `json:"stargazers_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("read the repository as JSON: %w", err)
	}
	return body.StargazersCount, nil
}
