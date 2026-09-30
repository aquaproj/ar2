package g2

import (
	"encoding/json"
	"strings"
	"testing"
)

// The API rejects a tree entry with no mode, including one that only takes a path out, so
// what is sent for a deletion is worth pinning down: the answer is a 422 rather than a
// file that stays.
func TestDeletedEntry(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(deletedEntry("versions/v1.0.0/registry-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"sha":null`,
		`"path":"versions/v1.0.0/registry-1.json"`,
		`"mode":"100644"`,
		`"type":"blob"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the entry doesn't say %s: %s", want, b)
		}
	}
}
