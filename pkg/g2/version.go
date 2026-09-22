package g2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// Version returns the registry.json the repository holds for one version of a
// package, or nil when it holds none.
func (c *Client) Version(ctx context.Context, pkgName, version string) (*aquag2.Registry, error) {
	path := VersionDir + "/" + version + "/registry.json"
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, path,
		&gogithub.RepositoryContentGetOptions{Ref: BranchName(pkgName)})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, nil //nolint:nilnil // the version isn't there, which isn't a failure
		}
		return nil, fmt.Errorf("get the generated registry.json: %w", err)
	}
	body, err := content.GetContent()
	if err != nil {
		return nil, fmt.Errorf("read the generated registry.json: %w", err)
	}
	reg := &aquag2.Registry{}
	if err := json.Unmarshal([]byte(body), reg); err != nil {
		return nil, fmt.Errorf("read the generated registry.json as JSON: %w", err)
	}
	return reg, nil
}
