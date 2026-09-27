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
		pkg, version, want string
	}{
		{pkg: "ogham/exa", version: "v0.8.0", want: "ar2_ogham_2fexa_v0.8.0"},
		{pkg: "grafana/loki/logcli", version: "v3.1.0", want: "ar2_grafana_2floki_2flogcli_v3.1.0"},
		{pkg: "cli/cli", version: "v2.0.0+build.1", want: "ar2_cli_2fcli_v2.0.0_2bbuild.1"},
	}
	for _, tt := range tests {
		t.Run(tt.pkg+"@"+tt.version, func(t *testing.T) {
			t.Parallel()
			if got := g2.VersionHeadBranchName(tt.pkg, tt.version); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
