// Package registry reads the aqua-registry (v1) registry.yaml.
// aqua-registry commits a merged registry.yaml at the repository root, so the whole
// package list can be fetched with a single request instead of walking pkgs/.
package registry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/go-github/v92/github"
	"gopkg.in/yaml.v3"
)

// Owner and Name identify the repository the package list is read from.
const (
	Owner = "aquaproj"
	Name  = "aqua-registry"
	// Path is the path of the merged registry.yaml in that repository.
	Path = "registry.yaml"
)

// APIPath returns the Contents API path of the merged registry.yaml at ref,
// relative to the API base URL.
//
// The Contents API is used rather than raw.githubusercontent.com because it takes the
// access token: the request then counts against the documented 5,000/hour bucket and
// reports errors as the rest of the API does, instead of against raw's undocumented
// limits for unauthenticated traffic. It also supports conditional requests, and a
// 304 costs no rate limit at all.
func APIPath(ref string) string {
	return fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", Owner, Name, Path, url.QueryEscape(ref))
}

// mediaTypeRaw makes the Contents API return the file itself rather than JSON with
// the content base64-encoded. registry.yaml is over 3 MB, past the 1 MB the JSON
// form allows.
const mediaTypeRaw = "application/vnd.github.raw"

// Registry is the subset of aqua-registry's registry.yaml that ar2 needs.
type Registry struct {
	Packages []*Package `yaml:"packages"`
}

// Package is the subset of a package definition that ar2 needs.
// Only the fields identifying the upstream GitHub repository are read; everything
// else is resolved from the release itself when registry.json is generated.
type Package struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	RepoOwner string `yaml:"repo_owner"`
	RepoName  string `yaml:"repo_name"`
}

// PackageName returns the package name. aqua-registry omits `name` when it is the
// same as "<repo_owner>/<repo_name>".
func (p *Package) PackageName() string {
	if p.Name != "" {
		return p.Name
	}
	if p.RepoOwner != "" && p.RepoName != "" {
		return p.RepoOwner + "/" + p.RepoName
	}
	return ""
}

// HasRepo reports whether the package refers to a GitHub repository.
// Packages without one (crates.io, http, ...) can't have a star count.
func (p *Package) HasRepo() bool {
	return p.RepoOwner != "" && p.RepoName != ""
}

// Client is the subset of go-github's client that Fetch uses.
// go-github has no method returning a file over 1 MB, so the request is built by
// hand; going through the client still gives its base URL, authentication, and
// error types such as *github.RateLimitError.
type Client interface {
	NewRequest(ctx context.Context, method, urlStr string, body any, opts ...github.RequestOption) (*http.Request, error)
	Do(req *http.Request, v any) (*github.Response, error)
}

// Fetch downloads and parses aqua-registry's merged registry.yaml at ref.
func Fetch(ctx context.Context, client Client, ref string) (*Registry, error) {
	req, err := client.NewRequest(ctx, http.MethodGet, APIPath(ref), nil)
	if err != nil {
		return nil, fmt.Errorf("create a request for registry.yaml: %w", err)
	}
	req.Header.Set("Accept", mediaTypeRaw)
	buf := &bytes.Buffer{}
	if _, err := client.Do(req, buf); err != nil {
		return nil, fmt.Errorf("get registry.yaml: %w", err)
	}
	return Parse(buf)
}

// Parse reads a registry.yaml.
func Parse(r io.Reader) (*Registry, error) {
	registry := &Registry{}
	if err := yaml.NewDecoder(r).Decode(registry); err != nil {
		return nil, fmt.Errorf("read registry.yaml as YAML: %w", err)
	}
	return registry, nil
}
