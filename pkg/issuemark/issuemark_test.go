package issuemark_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/issuemark"
	"github.com/google/go-cmp/cmp"
)

// A record written into an issue reads back as it was, among the prose around it, and a marker
// that can't be read is no record.
func TestMarker(t *testing.T) {
	t.Parallel()
	r := &issuemark.Record{
		Kind:     issuemark.KindMissingEnvironment,
		Package:  "kcp-dev/kcp",
		Versions: map[string][]string{"v0.33.1": {"windows/amd64", "windows/arm64"}},
	}
	marker, err := issuemark.Marker(r)
	if err != nil {
		t.Fatal(err)
	}
	text := "### v0.33.1\n\n- `windows/amd64`: <no asset>\n\n" + marker + "\n\n<!-- ar2-issue: {broken -->\n" +
		`<!-- ar2-issue: {"kind":"","package":"x"} -->`
	if diff := cmp.Diff([]*issuemark.Record{r}, issuemark.Parse(text)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
