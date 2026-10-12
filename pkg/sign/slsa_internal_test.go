package sign

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

func TestSLSASourceTag(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		slsa *aquaregistry.SLSAProvenance
		want string
	}{
		{name: "the entry says no tag", slsa: &aquaregistry.SLSAProvenance{SourceTag: "-"}, want: "-"},
		{name: "the entry names a tag", slsa: &aquaregistry.SLSAProvenance{SourceTag: "release-1.0"}, want: "release-1.0"},
		{name: "the entry says nothing", slsa: &aquaregistry.SLSAProvenance{}, want: "v1.26.0"},
		{name: "no provenance", slsa: nil, want: "v1.26.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := slsaSourceTag(tt.slsa, "v1.26.0"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
