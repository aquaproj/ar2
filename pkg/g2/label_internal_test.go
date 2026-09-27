package g2

import "testing"

// The label is what a pull request is found by later, so it says the version the way a
// release does whichever way the binary was built.
func TestLabelName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version string
		want    string
	}{
		{version: "v0.0.30", want: "ar2:v0.0.30"},
		{version: "0.0.30", want: "ar2:v0.0.30"},
		{version: "1.0.0-rc.1", want: "ar2:v1.0.0-rc.1"},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			if got := labelName(tt.version); got != tt.want {
				t.Errorf("labelName(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}
