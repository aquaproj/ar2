package g2_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
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

// One id can be the beginning of another: 1790772680 starts with 179077268. What tells a
// version branch of one from a version branch of the other is what follows the separator.
func TestIsVersionHeadBranch(t *testing.T) {
	t.Parallel()
	data := []struct {
		id     string
		branch string
		exp    bool
	}{
		{"179077268", "ar2_179077268_0.8.20", true},
		{"179077268", "ar2_1790772680_0.8.20", false},
		{"1790772680", "ar2_1790772680_0.8.20", true},
		// A version holds a slash of its own, which is escaped the way a name is.
		{"1790772903", "ar2_1790772903_kustomize_2fv5.8.1", true},
		// The package's own branch carries no version.
		{"1790772767", "ar2_1790772767", false},
		{"1790772767", "ar2_1790772767_", false},
		{"1790772767", "ar2_1790772767_v2.1.0", true},
		{"1790772767", "pkg_1790772767", false},
	}
	for _, d := range data {
		t.Run(d.id+" "+d.branch, func(t *testing.T) {
			t.Parallel()
			if got := g2.IsVersionHeadBranch(d.id, d.branch); got != d.exp {
				t.Fatalf("got %v, want %v", got, d.exp)
			}
		})
	}
}
