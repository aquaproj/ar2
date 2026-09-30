package g2

import (
	"context"
	"fmt"
	"net/http"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// ConfigFileName is the definition at the root of a package's branch, the file
// registry.json is generated from.
const ConfigFileName = aquag2.ConfigFileName

// ClaimDefinition is what a package branch is created holding: the name of the package and
// nothing else.
//
// The branch is named after an id, so nothing about it says which package it holds. The
// definition is where a branch answers for itself, and it has to answer from its first
// commit -- before that, a run that took the package over again would find no branch for it
// and make a second one. The conversion from aqua-registry replaces this with the definition
// itself, which is the moment the package is taken over.
func ClaimDefinition(pkgName string) string {
	return "name: " + pkgName + "\n"
}

// IsClaim reports whether a definition is only a branch's claim to a package, rather than a
// definition to generate from.
//
// A definition says where the package comes from; the claim says nothing but its name.
func IsClaim(cfg *aquag2.Config) bool {
	if cfg == nil || cfg.PackageInfo == nil {
		return false
	}
	return cfg.RepoOwner == "" && cfg.RepoName == "" && cfg.Type == ""
}

// Config returns the package's definition, or nil when its branch has none.
//
// A package without one is a package aqua-registry-g2 hasn't taken over yet: its
// definition still lives in aqua-registry, and the next pull request for it carries
// the converted one along with the generated files.
func (c *Client) Config(ctx context.Context, pkgName string) (*aquag2.Config, error) {
	branch, ok := c.Branch(pkgName)
	if !ok {
		return nil, nil //nolint:nilnil // no branch, so no definition
	}
	return c.ConfigOnRef(ctx, branch)
}

// ConfigOnRef returns the definition a ref holds, or nil when it holds none.
//
// The ref matters when the definition being generated from isn't the one the registry has
// merged: a pull request that carries a definition somebody has just written is the only place
// that version of it exists.
func (c *Client) ConfigOnRef(ctx context.Context, ref string) (*aquag2.Config, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, ConfigFileName,
		&gogithub.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		// No file, or no branch at all for a package nothing has been generated
		// for yet. Either way the definition isn't there.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("get the package definition: %w", err)
	}
	body, err := content.GetContent()
	if err != nil {
		return nil, fmt.Errorf("read the package definition: %w", err)
	}
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal([]byte(body), cfg); err != nil {
		return nil, fmt.Errorf("read the package definition as YAML: %w", err)
	}
	return cfg, nil
}
