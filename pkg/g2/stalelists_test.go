package g2_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
)

// fakeLists answers for the packages' directories, their versions directories' shas and their
// lists, by expression.
type fakeLists struct {
	dirs  []string
	oids  map[string]string
	blobs map[string]string
}

func (f *fakeLists) Subtrees(context.Context, string) ([]string, error) { return f.dirs, nil }

func (f *fakeLists) OIDs(_ context.Context, exprs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range exprs {
		if v, ok := f.oids[e]; ok {
			out[e] = v
		}
	}
	return out, nil
}

func (f *fakeLists) Blobs(_ context.Context, _ *slog.Logger, exprs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range exprs {
		if v, ok := f.blobs[e]; ok {
			out[e] = v
		}
	}
	return out, nil
}

// A list is behind when its source isn't its directory's sha, which is also a package with
// versions and no list, one with a list and no versions, and one whose list can't be read.
func TestStaleVersionsLists(t *testing.T) {
	t.Parallel()
	dir := func(id string) string { return "main:" + g2.PackageDir(id) }
	repo := &fakeLists{
		dirs: []string{"01/1790000001", "02/1790000002", "03/1790000003", "04/1790000004", "05/1790000005", "06/1790000006", "07/not-a-package"},
		oids: map[string]string{
			dir("1790000001") + "/versions": "a", // in step
			dir("1790000002") + "/versions": "b", // behind
			dir("1790000003") + "/versions": "c", // no list
			dir("1790000005") + "/versions": "e", // a list that can't be read
		},
		blobs: map[string]string{
			dir("1790000001") + "/versions.json": `{"source":"a","versions":[]}`,
			dir("1790000002") + "/versions.json": `{"source":"old","versions":[]}`,
			dir("1790000004") + "/versions.json": `{"source":"d","versions":[]}`, // no versions
			dir("1790000005") + "/versions.json": `{broken`,
			// 1790000006 has neither, which is in step.
		},
	}
	got, err := g2.StaleVersionsLists(context.Background(), slog.New(slog.DiscardHandler), repo)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"1790000002", "1790000003", "1790000004", "1790000005"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
