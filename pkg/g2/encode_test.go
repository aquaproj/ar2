package g2_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/g2"
)

func TestEncodePackageName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pkg  string
		want string
	}{
		{name: "a slash", pkg: "cli/cli", want: "cli_2fcli"},
		{name: "two slashes", pkg: "ipinfo/cli/grepip", want: "ipinfo_2fcli_2fgrepip"},
		{
			// "~" can't appear in a git ref at all, so it has to be escaped rather
			// than left alone.
			name: "a character a ref can't hold",
			pkg:  "sr.ht/~charles/rq",
			want: "sr.ht_2f_7echarles_2frq",
		},
		{
			// The underscore is escaped too, which is what makes the mapping
			// reversible.
			name: "an underscore",
			pkg:  "po3rin/github_link_creator",
			want: "po3rin_2fgithub_5flink_5fcreator",
		},
		{name: "nothing to escape", pkg: "aqua-registry.v2", want: "aqua-registry.v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, g2.EncodePackageName(tt.pkg)); diff != "" {
				t.Errorf("EncodePackageName is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEncodePackageName_prefixPairs checks the pairs that a "/" -> "__" scheme could
// not represent: git refuses to hold refs/heads/a and refs/heads/a/b at the same
// time, and aqua-registry has 30 package pairs in that shape.
func TestEncodePackageName_prefixPairs(t *testing.T) {
	t.Parallel()
	a := g2.BranchName("ipinfo/cli")
	b := g2.BranchName("ipinfo/cli/grepip")
	if a == b {
		t.Fatalf("the two packages encode to the same branch: %s", a)
	}
	for _, name := range []string{a, b} {
		for _, c := range name {
			if c == '/' {
				t.Errorf("a branch name must be a single segment, got %s", name)
			}
		}
	}
}
