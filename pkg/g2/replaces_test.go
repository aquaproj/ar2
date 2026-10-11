package g2_test

import (
	"strings"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
)

// The description names the label the checks wait for, and what to read before putting it on.
func TestReplacesPublished(t *testing.T) {
	t.Parallel()
	got := g2.ReplacesPublished("the diff.")
	for _, want := range []string{"`replaces-published`", "read the diff."} {
		if !strings.Contains(got, want) {
			t.Errorf("%q isn't in:\n%s", want, got)
		}
	}
}
