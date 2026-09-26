package g2

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// errVersionsTruncated is returned when the branch holds more files under versions/ than
// one request returns. Acting on a partial list would leave some of them behind.
var errVersionsTruncated = errors.New("the versions directory is too large to list in one request")

// VersionFiles returns every file the package's branch holds under versions/, as paths
// from the root of the branch.
//
// Every file rather than the registry.json of each version. What has to go when a package
// stops being served is whatever is there, and a version directory holding something this
// doesn't know about is exactly what would be left behind by a list built from names.
func (c *Client) VersionFiles(ctx context.Context, pkgName string) ([]string, error) {
	tree, resp, err := c.gh.Git.GetTree(ctx, c.owner, c.repo,
		BranchName(pkgName)+":"+aquag2.VersionDir, true)
	if err != nil {
		// No branch, or a branch that has never had a version generated onto it.
		// Either way it holds nothing under versions/.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get the versions directory of a package branch: %w", err)
	}
	if tree.GetTruncated() {
		return nil, errVersionsTruncated
	}
	paths := make([]string, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		if entry.GetType() != blobType {
			// A directory, which goes when the files in it do.
			continue
		}
		// The tree was addressed at versions/, so the paths come back relative to it.
		paths = append(paths, aquag2.VersionDir+"/"+entry.GetPath())
	}
	return paths, nil
}
