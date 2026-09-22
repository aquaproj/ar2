package verify

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

func TestNeedsChecksum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		typ  string
		want bool
	}{
		{typ: aquaregistry.PkgInfoTypeGitHubRelease, want: true},
		{typ: aquaregistry.PkgInfoTypeHTTP, want: true},
		// These build from source through another tool that verifies for itself, so
		// there is no artifact to hash.
		{typ: aquaregistry.PkgInfoTypeGoInstall, want: false},
		{typ: aquaregistry.PkgInfoTypeCargo, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			t.Parallel()
			if got := needsChecksum(&generate.Asset{Type: tt.typ}); got != tt.want {
				t.Errorf("needsChecksum(%q) is %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

func TestSetChecksum(t *testing.T) {
	t.Parallel()
	t.Run("records a missing checksum", func(t *testing.T) {
		t.Parallel()
		a := &generate.Asset{}
		if err := setChecksum(a, "abc"); err != nil {
			t.Fatal(err)
		}
		if a.Checksum != "abc" || a.ChecksumAlgorithm != ChecksumAlgorithm {
			t.Errorf("the checksum wasn't recorded: %+v", a)
		}
	})
	t.Run("accepts a digest that matches", func(t *testing.T) {
		t.Parallel()
		if err := setChecksum(&generate.Asset{Checksum: "abc"}, "abc"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects a digest that doesn't", func(t *testing.T) {
		t.Parallel()
		// Disagreeing means the asset was replaced after it was published, which is
		// the case a checksum exists to catch.
		if err := setChecksum(&generate.Asset{Checksum: "abc", Asset: "a.tar.gz"}, "def"); err == nil {
			t.Error("a digest that doesn't match the bytes must be an error")
		}
	})
}
