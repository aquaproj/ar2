package g2

import (
	"testing"

	"github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// A field is allowed when template-1 lists it, and a field below a type holding itself is
// allowed when its tail is, since the list spells such a type from where it first appears.
func TestAllowed(t *testing.T) {
	t.Parallel()
	fields := map[string]struct{}{
		"url":                           {},
		"version_overrides[].url":       {},
		"overrides[].format":            {},
		"version_overrides[].overrides": {},
	}
	for field, want := range map[string]bool{
		"url":                                    true,
		"version_overrides[].url":                true,
		"version_overrides[].overrides[].format": true,
		"asset":                                  false,
		"version_overrides[].asset":              false,
	} {
		if got := allowed(field, fields); got != want {
			t.Errorf("allowed(%q) = %v, want %v", field, got, want)
		}
	}
}

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
