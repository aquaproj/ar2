package sign

import (
	"testing"

	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// minisign isn't built for every host, and a check that can't run is not a check that
// failed: aqua skips it at install time on the same machines.
func TestMinisignUnrunnable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos, goarch string
		want         bool
	}{
		{goos: "linux", goarch: "amd64", want: false},
		{goos: "darwin", goarch: "arm64", want: false},
		{goos: "linux", goarch: "arm64", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			t.Parallel()
			v := &Verifier{rt: &runtime.Runtime{GOOS: tt.goos, GOARCH: tt.goarch}}
			if got := v.minisignUnrunnable() != ""; got != tt.want {
				t.Errorf("on %s/%s unrunnable is %v, want %v", tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}
