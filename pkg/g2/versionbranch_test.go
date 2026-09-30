package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
)

// A branch per version, so that a version that can't merge doesn't sit in the same pull
// request as the ones that can.
func TestVersionHeadBranchName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id, version, want string
	}{
		{id: "1790772767", version: "v0.8.0", want: "ar2_1790772767_v0.8.0"},
		// A version holds a slash of its own, which is escaped the way a name is.
		{id: "1790772903", version: "kustomize/v5.8.1", want: "ar2_1790772903_kustomize_2fv5.8.1"},
		{id: "1790772768", version: "v2.0.0+build.1", want: "ar2_1790772768_v2.0.0_2bbuild.1"},
	}
	for _, tt := range tests {
		t.Run(tt.id+"@"+tt.version, func(t *testing.T) {
			t.Parallel()
			if got := g2.VersionHeadBranchName(tt.id, tt.version); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
