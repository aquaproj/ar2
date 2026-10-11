package run

import (
	"errors"
	"testing"

	"github.com/aquaproj/ar2/pkg/verify"
)

// A version every environment of which the release has nothing for is a failure to generate,
// and one whose archive lacks a file the definition names still waits for a definition.
func TestPublishedNothing(t *testing.T) {
	t.Parallel()
	excluded := []*verify.Excluded{{Reason: "download the asset: status code 404"}}
	if err := publishedNothing(&verify.Filled{Excluded: excluded}); !errors.Is(err, errPublishedNothing) {
		t.Errorf("every environment refused: got %v", err)
	}
	unresolved := &verify.Filled{Excluded: excluded, Unresolved: []string{"linux/amd64: foo (the archive holds bar)"}}
	if err := publishedNothing(unresolved); err != nil {
		t.Errorf("a file the archive lacks: got %v", err)
	}
	if err := publishedNothing(&verify.Filled{}); err != nil {
		t.Errorf("nothing excluded: got %v", err)
	}
}
