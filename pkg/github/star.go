// Package github fetches data from GitHub's GraphQL API.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const endpoint = "https://api.github.com/graphql"

// BatchSize is the number of repositories queried in one GraphQL request.
// GitHub charges GraphQL by node count rather than by request, so batching turns a
// sweep over ~2,300 packages from that many REST calls into ~46 requests.
//
// 100 aliases per query makes GitHub answer some of them with
// RESOURCE_LIMITS_EXCEEDED, which silently drops those repositories from the result.
// 75 and below were clean when measured, and 50 leaves room for the limit to move.
const BatchSize = 50

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string {
	return r.Owner + "/" + r.Name
}

// Client queries GitHub's GraphQL API.
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// NewClient creates a Client. httpClient must attach the GitHub token.
func NewClient(httpClient *http.Client) *Client {
	return &Client{
		httpClient: httpClient,
		endpoint:   endpoint,
	}
}

// GetStars returns the stargazer count of each repository.
// Repositories that can't be read (deleted, renamed, private) are left out of the
// result rather than failing the whole batch, because one missing package must not
// stop the rest from being initialized.
func (c *Client) GetStars(ctx context.Context, repos []Repo) (map[string]int, error) {
	stars := make(map[string]int, len(repos))
	for start := 0; start < len(repos); start += BatchSize {
		end := min(start+BatchSize, len(repos))
		if err := c.getStars(ctx, repos[start:end], stars); err != nil {
			return nil, err
		}
	}
	return stars, nil
}

// aliasPrefix keeps the GraphQL aliases valid: an alias can't start with a digit.
const aliasPrefix = "r"

// argsPerRepo is the number of GraphQL variables each repository needs: its owner
// and its name.
const argsPerRepo = 2

func buildQuery(repos []Repo) (string, map[string]any) {
	args := make([]string, 0, len(repos)*argsPerRepo)
	fields := make([]string, 0, len(repos))
	vars := make(map[string]any, len(repos)*argsPerRepo)
	for i, repo := range repos {
		alias := aliasPrefix + strconv.Itoa(i)
		ownerVar, nameVar := alias+"o", alias+"n"
		args = append(args, "$"+ownerVar+": String!", "$"+nameVar+": String!")
		fields = append(fields, fmt.Sprintf("%s: repository(owner: $%s, name: $%s) { stargazerCount }", alias, ownerVar, nameVar))
		vars[ownerVar] = repo.Owner
		vars[nameVar] = repo.Name
	}
	query := "query(" + strings.Join(args, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}"
	return query, vars
}

type graphQLResponse struct {
	Data   map[string]*repository `json:"data"`
	Errors []graphQLError         `json:"errors"`
}

type repository struct {
	StargazerCount int `json:"stargazerCount"`
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	// Path names the alias the error belongs to. Its first element is the alias.
	Path []any `json:"path"`
}

// alias returns the alias the error belongs to, or "" if the error is not tied to one.
func (e *graphQLError) alias() string {
	if len(e.Path) == 0 {
		return ""
	}
	s, _ := e.Path[0].(string)
	return s
}

// errResourceLimits is the error type GitHub returns when a query asks for too much
// in one request. It says nothing about the repository, so the same repository
// succeeds in a smaller batch.
const errResourceLimits = "RESOURCE_LIMITS_EXCEEDED"

func (c *Client) getStars(ctx context.Context, repos []Repo, stars map[string]int) error {
	result, err := c.request(ctx, repos)
	if err != nil {
		return err
	}
	// A batch can carry errors for some aliases while still returning data for the
	// rest, so errors are only fatal when nothing came back.
	if len(result.Data) == 0 && len(result.Errors) > 0 {
		return fmt.Errorf("GraphQL request failed: %s", result.Errors[0].Message)
	}
	// Repositories whose alias hit the resource limit are retried in smaller batches.
	// They are not missing: the same query succeeds when it asks for less.
	overflowed := collect(repos, result, stars)
	if len(overflowed) == 0 {
		return nil
	}
	if len(overflowed) == len(repos) {
		// Splitting would recurse forever on a batch that never shrinks.
		return fmt.Errorf("GraphQL resource limits exceeded for %d repositories", len(overflowed))
	}
	return c.getStars(ctx, overflowed, stars)
}

// collect reads the star counts out of a response and returns the repositories that
// have to be asked for again because their alias hit the resource limit.
// Anything else left unanswered (NOT_FOUND) is a repository that can't be resolved;
// it is left out of the result and reported by the caller.
func collect(repos []Repo, result *graphQLResponse, stars map[string]int) []Repo {
	limited := limitedAliases(result.Errors)
	var overflowed []Repo
	for i, repo := range repos {
		alias := aliasPrefix + strconv.Itoa(i)
		if r := result.Data[alias]; r != nil {
			stars[repo.String()] = r.StargazerCount
			continue
		}
		if _, ok := limited[alias]; ok {
			overflowed = append(overflowed, repo)
		}
	}
	return overflowed
}

// request sends one GraphQL query asking for the star count of each repository.
func (c *Client) request(ctx context.Context, repos []Repo) (*graphQLResponse, error) {
	query, vars := buildQuery(repos)
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": vars,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal a GraphQL request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create a GraphQL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send a GraphQL request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("send a GraphQL request: status code %d", resp.StatusCode)
	}
	result := &graphQLResponse{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, fmt.Errorf("read a GraphQL response as JSON: %w", err)
	}
	return result, nil
}

// limitedAliases returns the aliases that failed with RESOURCE_LIMITS_EXCEEDED.
func limitedAliases(errs []graphQLError) map[string]struct{} {
	m := map[string]struct{}{}
	for _, e := range errs {
		if e.Type != errResourceLimits {
			continue
		}
		if alias := e.alias(); alias != "" {
			m[alias] = struct{}{}
		}
	}
	return m
}
