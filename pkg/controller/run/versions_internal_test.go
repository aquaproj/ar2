package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// A tag that names a place in a release history rather than a point in it moves as the
// repository releases. What a run generates is what a tag points at -- the asset name,
// the checksum, the files inside the archive -- so generated for a moving tag all of
// it stops being true the next time upstream releases.
//
// neovim's stable is the one this was found on. It has no version_filter, so nothing
// else was going to drop it.
func TestFilterVersions_movingTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		base *aquaregistry.PackageInfo
		want []string
	}{
		{
			name: "no version_filter",
			base: nil,
			want: []string{"v0.12.5", "v0.12.4", "v1.0.0-stable"},
		},
		{
			// The filter and the moving tags are separate reasons, and both apply.
			name: "a version_filter as well",
			base: &aquaregistry.PackageInfo{VersionFilter: `Version matches "^v0\\."`},
			want: []string{"v0.12.5", "v0.12.4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := filterVersions(discardLogger(),
				[]string{"stable", "v0.12.5", "nightly", "v0.12.4", "latest", "v1.0.0-stable"}, tt.base)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the versions are wrong (-want +got):\n%s", diff)
			}
		})
	}
}
