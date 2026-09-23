package g2

import (
	"context"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// RegistryConfigFileName is the registry's own configuration, on the default branch.
//
// It says what ar2 should do differently for this registry, which is nothing about
// any one package's contents: those live on the package's branch. Today that is the
// list of packages to leave alone.
const RegistryConfigFileName = "ar2.yaml"

// RegistryConfig is what RegistryConfigFileName holds.
type RegistryConfig struct {
	// IgnoredPackages are the packages a run doesn't look at.
	//
	// A package that can't be generated is tried again on every run: it costs API
	// calls, and it fills the summary of what wasn't published with the same names
	// until whoever reads it stops reading. A repository that was deleted or renamed
	// will never generate, and a package somebody decided not to migrate never
	// should, and neither is a thing to rediscover every half hour.
	IgnoredPackages []*IgnoredPackage `yaml:"ignored_packages,omitempty"`
}

// IgnoredPackage is a package to leave alone, and why.
type IgnoredPackage struct {
	Name string `yaml:"name"`
	// Reason is for whoever reads the file later, which is usually the person
	// wondering why a package they expected isn't there.
	Reason string `yaml:"reason,omitempty"`
}

// Ignored returns the names to leave alone, as a set.
func (c *RegistryConfig) Ignored() map[string]struct{} {
	if c == nil {
		return nil
	}
	ignored := make(map[string]struct{}, len(c.IgnoredPackages))
	for _, pkg := range c.IgnoredPackages {
		if pkg != nil && pkg.Name != "" {
			ignored[pkg.Name] = struct{}{}
		}
	}
	return ignored
}

// RegistryConfig returns the registry's configuration, or an empty one when the
// repository has none. A registry that asks for nothing different needs no file.
func (c *Client) RegistryConfig(ctx context.Context, ref string) (*RegistryConfig, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, RegistryConfigFileName,
		&gogithub.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return &RegistryConfig{}, nil
		}
		return nil, fmt.Errorf("get the registry configuration: %w", err)
	}
	body, err := content.GetContent()
	if err != nil {
		return nil, fmt.Errorf("read the registry configuration: %w", err)
	}
	cfg := &RegistryConfig{}
	if err := yaml.Unmarshal([]byte(body), cfg); err != nil {
		return nil, fmt.Errorf("read the registry configuration as YAML: %w", err)
	}
	return cfg, nil
}
