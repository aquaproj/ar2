package g2

import (
	"testing"

	"github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// What a definition sets is reported by the names aqua writes, maps and lists included.
func TestUsedFields(t *testing.T) {
	t.Parallel()
	url := "https://example.com"
	got := usedFields(&registry.PackageInfo{
		Type:         "http",
		URL:          url,
		Replacements: registry.Replacements{"darwin": "macos"},
		Files:        []*registry.File{{Name: "cli"}},
	})
	want := []string{"files[].name", "replacements{}", "type", "url"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error(diff)
	}
}
