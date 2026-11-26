package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

// airbytehq/abctl's definition leaves airbox's releases out, and aqua-registry's says
// nothing about them: the versions listed are the definition's to decide.
func TestFilterVersions_definitionDecides(t *testing.T) {
	t.Parallel()
	base := &aquaregistry.PackageInfo{RepoOwner: "airbytehq", RepoName: "abctl"}
	def := &definition{config: &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		RepoOwner:     "airbytehq",
		RepoName:      "abctl",
		VersionFilter: `not (Version startsWith "airbox-")`,
	}}}
	tags := []string{"v0.30.4", "airbox-v0.1.1", "airbox-v0.1.0", "v0.30.3"}
	tests := []struct {
		name string
		def  *definition
		want []string
	}{
		{name: "the registry's definition", def: def, want: []string{"v0.30.4", "v0.30.3"}},
		{name: "no definition to go by", want: tags},
		{name: "a definition with nothing in it", def: &definition{}, want: tags},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := filterVersions(discardLogger(), tags, versionRules(base, tt.def))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
