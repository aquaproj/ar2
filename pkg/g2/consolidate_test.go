package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

// Each package lands in its directory as the objects its branch holds, with the mode and type
// they had: a file stays a file and the versions directory stays a tree, named by its sha.
func TestConsolidatedEntries(t *testing.T) {
	t.Parallel()
	entry := func(path, mode, typ, sha string) *gogithub.TreeEntry {
		return &gogithub.TreeEntry{Path: new(path), Mode: new(mode), Type: new(typ), SHA: new(sha)}
	}
	got := g2.ConsolidatedEntries([]*g2.LegacyPackage{{
		ID:     "1790772769",
		Branch: "pkg_1790772769",
		Entries: []*gogithub.TreeEntry{
			entry("registry.yaml", "100644", "blob", "def"),
			entry("versions", "040000", "tree", "dir"),
		},
	}})
	want := []*gogithub.TreeEntry{
		entry("pkgs/69/1790772769/registry.yaml", "100644", "blob", "def"),
		entry("pkgs/69/1790772769/versions", "040000", "tree", "dir"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
