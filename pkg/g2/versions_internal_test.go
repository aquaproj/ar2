package g2

import (
	"testing"

	gogithub "github.com/google/go-github/v92/github"
)

func TestVersionOf(t *testing.T) {
	t.Parallel()
	entry := func(typ, path string) *gogithub.TreeEntry {
		return &gogithub.TreeEntry{Type: &typ, Path: &path}
	}
	tests := []struct {
		name  string
		entry *gogithub.TreeEntry
		want  string
		ok    bool
	}{
		{
			name:  "the generated file of a version",
			entry: entry("blob", "v1.0.0/registry-1.json"),
			want:  "v1.0.0",
			ok:    true,
		},
		{
			// A version whose tag has a slash in it is escaped into one directory, the
			// way a package name is escaped into one branch.
			name:  "an escaped version",
			entry: entry("blob", "kustomize_2fv5.8.1/registry-1.json"),
			want:  "kustomize/v5.8.1",
			ok:    true,
		},
		{
			// The directory itself says nothing about whether the version is there.
			name:  "the directory of a version",
			entry: entry("tree", "v1.0.0"),
		},
		{
			// Before the version was escaped, a slash in it became two directories.
			// Reporting the outer one as generated hid the version that is there and
			// claimed one that doesn't exist.
			name:  "a directory left by the layout that didn't escape",
			entry: entry("blob", "kustomize/v5.8.1/registry-1.json"),
		},
		{
			name:  "something else on the branch",
			entry: entry("blob", "v1.0.0/README.md"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := versionOf(tt.entry)
			if ok != tt.ok {
				t.Fatalf("got ok=%v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
