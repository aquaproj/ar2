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

// Config returns the package's definition, or nil when its branch has none.
//
// A package without one is a package aqua-registry-g2 hasn't taken over yet: its
// definition still lives in aqua-registry, and the next pull request for it carries
// the converted one along with the generated files.
func (c *Client) Config(ctx context.Context, pkgName string) (*aquag2.Config, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, ConfigFileName,
		&gogithub.RepositoryContentGetOptions{Ref: BranchName(pkgName)})
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
