package g2

import (
	"context"
	"fmt"
	"net/http"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// ConfigFileName is the definition at the root of a package's branch, the file
// registry.json is generated from.
const ConfigFileName = aquag2.ConfigFileName

// HasConfig reports whether the package's branch already has its definition.
//
// A package that doesn't is one aqua-registry-g2 hasn't taken over yet: its
// definition still lives in aqua-registry, and the next pull request for it carries
// the converted one along with the generated files.
func (c *Client) HasConfig(ctx context.Context, pkgName string) (bool, error) {
	_, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, ConfigFileName,
		&gogithub.RepositoryContentGetOptions{Ref: BranchName(pkgName)})
	if err != nil {
		// No file, or no branch at all for a package nothing has been generated
		// for yet. Either way the definition isn't there.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("get the package definition: %w", err)
	}
	return true, nil
}
