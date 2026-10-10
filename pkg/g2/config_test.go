package g2_test

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

// repo_id goes right after repo_name rather than after every field aqua's type inlines.
func TestMarshalConfig(t *testing.T) {
	t.Parallel()
	got, err := g2.MarshalConfig(&aquag2.Config{
		PackageInfo: &aquaregistry.PackageInfo{
			Name:        "cli/cli",
			Type:        "github_release",
			RepoOwner:   "cli",
			RepoName:    "cli",
			Description: "GitHub's official command line tool",
			Files:       []*aquaregistry.File{{Name: "gh"}},
		},
		RepoID: 212613049,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `name: cli/cli
type: github_release
repo_owner: cli
repo_name: cli
repo_id: 212613049
description: GitHub's official command line tool
files:
  - name: gh
`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
