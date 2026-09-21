package generate

import (
	"slices"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/google/go-cmp/cmp"
)

// TestExpandByVariants checks that a package shipping separate musl and glibc builds
// produces a runtime for each. Enumerating os/arch alone resolves only whichever
// sibling override matches first, which silently loses the other build.
func TestExpandByVariants(t *testing.T) {
	t.Parallel()
	pkgInfo := &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{
			{
				GOOS:     "linux",
				Variants: aquaregistry.Variants{{Key: "libc", Value: "glibc"}},
			},
			{
				GOOS:     "linux",
				Asset:    "pkg-musl",
				Variants: aquaregistry.Variants{{Key: "libc", Value: "musl"}},
			},
		},
	}
	got := expandByVariants(pkgInfo, []*aquaruntime.Runtime{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
	})
	keys := make([]string, 0, len(got))
	for _, rt := range got {
		keys = append(keys, rt.GOOS+"/"+rt.GOARCH+"/"+rt.LibC)
	}
	want := []string{"linux/amd64/glibc", "linux/amd64/musl", "darwin/arm64/"}
	slices.Sort(want)
	slices.Sort(keys)
	if diff := cmp.Diff(want, keys); diff != "" {
		t.Errorf("expandByVariants is wrong (-want +got):\n%s", diff)
	}
}

// TestExpandByVariants_noVariants checks that a package without variant-aware
// overrides is left alone.
func TestExpandByVariants_noVariants(t *testing.T) {
	t.Parallel()
	pkgInfo := &aquaregistry.PackageInfo{
		Overrides: []*aquaregistry.Override{{GOOS: "windows", Format: "zip"}},
	}
	rts := []*aquaruntime.Runtime{{GOOS: "windows", GOARCH: "amd64"}}
	got := expandByVariants(pkgInfo, rts)
	if len(got) != 1 || got[0].LibC != "" {
		t.Errorf("a package without variants should not be expanded, got %+v", got)
	}
}

func TestVariantsOf(t *testing.T) {
	t.Parallel()
	if got := variantsOf(&aquaruntime.Runtime{GOOS: "linux"}); got != nil {
		t.Errorf("a runtime without a libc should have no variants, got %v", got)
	}
	want := map[string]string{"libc": "musl"}
	got := variantsOf(&aquaruntime.Runtime{GOOS: "linux", LibC: "musl"})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("variantsOf is wrong (-want +got):\n%s", diff)
	}
}
