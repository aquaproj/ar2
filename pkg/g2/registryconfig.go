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
	// Breadth is how much of a run one package may take while it has little in the
	// registry.
	Breadth *Breadth `yaml:"breadth,omitempty"`
}

// Breadth is how far a run goes with a package the registry holds little of.
//
// A run is bounded, and every package wants all of its versions. Spending the whole
// run on the first one leaves the rest with nothing at all, and a package with
// nothing can't be installed from the registry; so a package short of Versions gets
// enough of a turn to reach it and the run moves on.
//
// What the right number is changes. While the registry is being filled it wants to be
// small, so that every package gets its newest versions before any package gets its
// history. Once that is done the packages arriving are new ones, and a new package
// with two usable versions is barely better than one with none, so it wants to be
// larger. The registry says which it is, because nothing here can tell.
type Breadth struct {
	// Versions ends the turn once the package has that many. Default 5.
	Versions int `yaml:"versions,omitempty"`
	// Attempts ends the turn after that many versions have been tried, whether or
	// not they worked.
	//
	// A release with no assets, or one whose signature can't be verified yet,
	// produces nothing, and the newest versions are the likeliest to -- which are
	// the ones a turn starts from. Without this a package stops short of Versions
	// every turn and takes one every lap forever.
	//
	// Unset, a turn tries exactly as many versions as it wants and no more, so a
	// version that fails costs the package a lap.
	Attempts int `yaml:"attempts,omitempty"`
}

// defaultBreadthVersions is how many versions a package gets before the run moves on,
// when the registry doesn't say.
//
// Small, because the first thing the registry needs is for every package to be
// installable at all. Someone installing a package almost always wants a recent
// version, and a package with nothing can't be installed from g2 at all.
const defaultBreadthVersions = 5

// GetVersions is how many versions ends a package's turn.
func (b *Breadth) GetVersions() int {
	if b == nil || b.Versions <= 0 {
		return defaultBreadthVersions
	}
	return b.Versions
}

// GetAttempts is how many versions may be tried to get there, or zero for as many as
// are wanted and no more.
func (b *Breadth) GetAttempts() int {
	if b == nil || b.Attempts <= 0 {
		return 0
	}
	return b.Attempts
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
