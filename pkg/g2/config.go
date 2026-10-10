package g2

import (
	"context"
	"fmt"
	"net/http"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// ConfigFileName is the definition in a package's directory, the file registry.json is
// generated from.
const ConfigFileName = aquag2.ConfigFileName

// ClaimDefinition is a definition holding the name of the package and nothing else.
//
// It is what a package branch used to be created holding, before its definition arrived,
// so that the branch named after an id said which package it held. Nothing writes one any
// more, and the packages copied from those branches may still hold one.
func ClaimDefinition(pkgName string) string {
	return "name: " + pkgName + "\n"
}

// IsClaim reports whether a definition is only a claim to a package, rather than a
// definition to generate from.
//
// A definition says where the package comes from; the claim says nothing but its name.
func IsClaim(cfg *aquag2.Config) bool {
	if cfg == nil || cfg.PackageInfo == nil {
		return false
	}
	return cfg.RepoOwner == "" && cfg.RepoName == "" && cfg.Type == ""
}

// Config returns the package's definition, or nil when the registry holds none.
//
// A package without one is a package aqua-registry-g2 hasn't taken over yet: its
// definition still lives in aqua-registry, and the next pull request for it carries
// the converted one along with the generated files.
func (c *Client) Config(ctx context.Context, pkgName string) (*aquag2.Config, error) {
	return c.ConfigOnRef(ctx, DefaultBranch, pkgName)
}

// ConfigOnRef returns the package's definition on a ref, or nil when it holds none.
//
// The ref matters when the definition being generated from isn't the one the registry has
// merged: a pull request that carries a definition somebody has just written is the only place
// that version of it exists.
func (c *Client) ConfigOnRef(ctx context.Context, ref, pkgName string) (*aquag2.Config, error) {
	dir, ok := c.Dir(pkgName)
	if !ok {
		return nil, nil //nolint:nilnil // a package the registry doesn't hold has no definition
	}
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, dir+"/"+ConfigFileName,
		&gogithub.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		// No file, or no branch at all. Either way the definition isn't there.
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
