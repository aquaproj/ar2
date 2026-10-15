package g2

import (
	"log/slog"
	"testing"
)

// The list says a version is a prerelease when the file the registry serves for it does,
// so a reader of the list can pass it over without reading every file.
func TestVersionEntry_prerelease(t *testing.T) {
	t.Parallel()
	entry := versionEntry(slog.New(slog.DiscardHandler), "v2.0.0-rc.1",
		`{"published_at":"2026-10-01T00:00:00Z","prerelease":true,"assets":[]}`)
	if !entry.Prerelease {
		t.Error("the version isn't listed as a prerelease")
	}
}
