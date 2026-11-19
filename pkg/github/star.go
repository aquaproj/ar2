// Package github fetches data from GitHub's GraphQL API.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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
	// anonymousEndpoint is the REST API the star count of a refused repository is
	// read from. A test points it somewhere it controls: the real one answers
	// differently depending on how much of an hour's anonymous budget the machine
	// running the test has left, and a test that depends on that fails for reasons
	// that have nothing to do with the code.
	anonymousEndpoint string
	// retryWait is how long the first retry of a query waits, doubling after it. A test
	// sets it to nothing.
	retryWait time.Duration
}

// NewClient creates a Client. httpClient must attach the GitHub token.
func NewClient(httpClient *http.Client) *Client {
	return &Client{
		httpClient:        httpClient,
		endpoint:          endpoint,
		anonymousEndpoint: anonymousEndpoint,
		retryWait:         defaultRetryWait,
	}
}

// queryAttempts is how many times a query is sent before its failure is the run's.
const queryAttempts = 3

// defaultRetryWait is how long the first retry waits.
const defaultRetryWait = 5 * time.Second

// GetStars returns the stargazer count of each repository, and why any of them
// couldn't be read.
//
// A repository that can't be read is left out of the result rather than failing the
// whole batch, because one missing package must not stop the rest from being
// initialized. The reason is returned so the caller can say more than that it
// failed: a deleted repository and an organization refusing the request look the
// same from here otherwise.
//
// A batch that fails outright is treated the same way. Returning nothing would throw
// away every count already read, and building the state from scratch asks for more
// than two thousand of them in 47 batches: one failing request would leave every
// package looking equally unused and the run processing them in name order.
func (c *Client) GetStars(ctx context.Context, repos []Repo) (map[string]int, map[string]string, error) {
	stars := make(map[string]int, len(repos))
	reasons := map[string]string{}
	for start := 0; start < len(repos); start += BatchSize {
		end := min(start+BatchSize, len(repos))
		batch := repos[start:end]
		record := func(repo Repo, r *repository) { stars[repo.String()] = r.StargazerCount }
		if err := c.fetch(ctx, batch, starCount, record, reasons); err != nil {
			for _, repo := range batch {
				reasons[repo.String()] = err.Error()
			}
		}
	}
	return stars, reasons, nil
}

// aliasPrefix keeps the GraphQL aliases valid: an alias can't start with a digit.
const aliasPrefix = "r"

// argsPerRepo is the number of GraphQL variables each repository needs: its owner
// and its name.
const argsPerRepo = 2

// selection is what a query asks for about one repository, written as the body of
// its repository field.
type selection func(ownerVar, nameVar string) string

func buildQuery(repos []Repo, sel selection) (string, map[string]any) {
	args := make([]string, 0, len(repos)*argsPerRepo)
	fields := make([]string, 0, len(repos))
	vars := make(map[string]any, len(repos)*argsPerRepo)
	for i, repo := range repos {
		alias := aliasPrefix + strconv.Itoa(i)
		ownerVar, nameVar := alias+"o", alias+"n"
		args = append(args, "$"+ownerVar+": String!", "$"+nameVar+": String!")
		fields = append(fields, alias+": "+sel(ownerVar, nameVar))
		vars[ownerVar] = repo.Owner
		vars[nameVar] = repo.Name
	}
	query := "query(" + strings.Join(args, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}"
	return query, vars
}

// starCount asks for a repository's stargazer count.
func starCount(ownerVar, nameVar string) string {
	return fmt.Sprintf("repository(owner: $%s, name: $%s) { stargazerCount }", ownerVar, nameVar)
}

type graphQLResponse struct {
	Data   map[string]*repository `json:"data"`
	Errors []graphQLError         `json:"errors"`
}

// repository is what a query asks about one repository. Each query fills in the
// fields it selected and leaves the rest empty.
type repository struct {
	StargazerCount int `json:"stargazerCount"`
	// NameWithOwner is what the repository is called now. GitHub answers a query made
	// with an old name, so this differs from what was asked when the repository has
	// been renamed or transferred since the registry recorded it.
	NameWithOwner string `json:"nameWithOwner"`
	// DatabaseID is the repository's numeric id, which REST calls id.
	DatabaseID int64 `json:"databaseId"`
	// LatestRelease is the newest release that is neither a draft nor a prerelease, and
	// nil when the repository has published none.
	LatestRelease *struct {
		TagName string `json:"tagName"`
	} `json:"latestRelease"`
	Releases *struct {
		Nodes []struct {
			TagName      string `json:"tagName"`
			IsDraft      bool   `json:"isDraft"`
			IsPrerelease bool   `json:"isPrerelease"`
		} `json:"nodes"`
	} `json:"releases"`
	Refs *struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"refs"`
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

// take records what a query asked for about one repository.
type take func(repo Repo, r *repository)

// fetch asks one batch, records what came back, and asks again in smaller batches
// for the repositories whose alias hit the resource limit.
func (c *Client) fetch(ctx context.Context, repos []Repo, sel selection, record take, reasons map[string]string) error {
	result, err := c.request(ctx, repos, sel)
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
	overflowed := collect(repos, result, record, reasons)
	if len(overflowed) == 0 {
		return nil
	}
	if len(overflowed) == len(repos) {
		// Splitting would recurse forever on a batch that never shrinks.
		return fmt.Errorf("GraphQL resource limits exceeded for %d repositories", len(overflowed))
	}
	return c.fetch(ctx, overflowed, sel, record, reasons)
}

// collect reads the star counts out of a response and returns the repositories that
// have to be asked for again because their alias hit the resource limit.
// Anything else left unanswered (NOT_FOUND) is a repository that can't be resolved;
// it is left out of the result and reported by the caller.
func collect(repos []Repo, result *graphQLResponse, record take, reasons map[string]string) []Repo {
	limited := limitedAliases(result.Errors)
	byAlias := errorsByAlias(result.Errors)
	var overflowed []Repo
	for i, repo := range repos {
		alias := aliasPrefix + strconv.Itoa(i)
		if r := result.Data[alias]; r != nil {
			record(repo, r)
			delete(reasons, repo.String())
			continue
		}
		if _, ok := limited[alias]; ok {
			overflowed = append(overflowed, repo)
			continue
		}
		reasons[repo.String()] = byAlias[alias]
	}
	return overflowed
}

// errorsByAlias maps each alias to why it wasn't answered.
func errorsByAlias(errs []graphQLError) map[string]string {
	m := make(map[string]string, len(errs))
	for _, e := range errs {
		alias := e.alias()
		if alias == "" {
			continue
		}
		reason := e.Type
		if reason == "" {
			reason = e.Message
		}
		m[alias] = reason
	}
	return m
}

// request sends one GraphQL query asking the same thing about each repository.
func (c *Client) request(ctx context.Context, repos []Repo, sel selection) (*graphQLResponse, error) {
	query, vars := buildQuery(repos, sel)
	result := &graphQLResponse{}
	if err := c.post(ctx, query, vars, result); err != nil {
		return nil, err
	}
	return result, nil
}

// post sends one GraphQL query and reads the answer into out.
//
// A query GitHub answers with 502, 503 or 504 is sent again, a few seconds later and then
// a few more. Those are GitHub having a bad minute, and reading the registry's definitions
// is one query a run can't do without: one failure of it was a whole run lost.
func (c *Client) post(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": vars,
	})
	if err != nil {
		return fmt.Errorf("marshal a GraphQL request: %w", err)
	}
	wait := c.retryWait
	for attempt := 1; ; attempt++ {
		err := c.send(ctx, body, out)
		again, atLeast := retryable(err)
		if attempt == queryAttempts || !again {
			return err
		}
		next := max(wait, c.rateLimitWait(atLeast))
		select {
		case <-ctx.Done():
			return err
		case <-time.After(next):
		}
		wait *= 2
	}
}

// rateLimitWait is how long to wait out a rate limit that asked for d. A test that sets
// retryWait to nothing waits nothing here either.
func (c *Client) rateLimitWait(d time.Duration) time.Duration {
	if c.retryWait == 0 {
		return 0
	}
	return d
}

// send sends a GraphQL query once.
func (c *Client) send(ctx context.Context, body []byte, out any) error {
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
		return fmt.Errorf("send a GraphQL request: %w", newStatusError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("read a GraphQL response as JSON: %w", err)
	}
	return nil
}

// statusError is a GraphQL request answered with a status other than 200.
type statusError struct {
	code int
	// message is the start of what the answer said, so that a refusal says why: a 403 is a
	// token without the permission as often as it is a rate limit.
	message string
	// limited says GitHub answered that the request went over a rate limit, and retryAfter
	// is how long it asked for before the next one, zero when it didn't say.
	limited    bool
	retryAfter time.Duration
}

// maxMessage is how much of a refusal's body is kept.
const maxMessage = 300

func newStatusError(resp *http.Response) *statusError {
	e := &statusError{code: resp.StatusCode}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxMessage))
	e.message = strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return e
	}
	// What GitHub documents for both limits: a retry-after header, or the primary limit's
	// remaining count at zero with the time it resets, or neither, for a secondary limit
	// that says so only in the message.
	if v := resp.Header.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil {
			e.limited = true
			e.retryAfter = time.Duration(sec) * time.Second
		}
	}
	if resp.Header.Get("X-Ratelimit-Remaining") == "0" {
		e.limited = true
		if reset, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			if d := time.Until(time.Unix(reset, 0)); d > e.retryAfter {
				e.retryAfter = d
			}
		}
	}
	if strings.Contains(strings.ToLower(e.message), "rate limit") {
		e.limited = true
	}
	return e
}

func (e *statusError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("status code %d", e.code)
	}
	return fmt.Sprintf("status code %d: %s", e.code, e.message)
}

// maxRateLimitWait is the longest a query waits out a rate limit before its failure is the
// run's. A secondary limit asks for a minute or so; the primary one can reset an hour away,
// which no run waits for.
const maxRateLimitWait = 2 * time.Minute

// retryable says whether a failed query is worth sending again, and how long to wait at
// least before it is.
//
// A rate limit is worth it when GitHub asks for no longer than maxRateLimitWait. Opening a
// pull request a few seconds apart reaches the secondary limit now and then, and then turning
// on auto-merge was refused with a 403 and left ogen-go/ogen's v1.15.0 and TaKO8Ki/frum's
// v0.1.0 and v0.1.1 for a person, every check on them passing.
func retryable(err error) (bool, time.Duration) {
	e, ok := errors.AsType[*statusError](err)
	if !ok {
		return false, 0
	}
	switch e.code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true, 0
	case http.StatusForbidden, http.StatusTooManyRequests:
		if !e.limited || e.retryAfter > maxRateLimitWait {
			return false, 0
		}
		// GitHub asks for a minute when a secondary limit says nothing of when.
		if e.retryAfter == 0 {
			return true, time.Minute
		}
		return true, e.retryAfter
	}
	return false, 0
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
