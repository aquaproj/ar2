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

// One package's name can be the beginning of another's, and the parent's branch prefix
// then matches the child's version branches. GoogleCloudPlatform/terraformer was
// regenerated onto GoogleCloudPlatform/terraformer/aws's branch for it.
func TestIsVersionHeadBranch(t *testing.T) {
	t.Parallel()
	data := []struct {
		pkg    string
		branch string
		exp    bool
	}{
		{"GoogleCloudPlatform/terraformer", "ar2_GoogleCloudPlatform_2fterraformer_0.8.20", true},
		{"GoogleCloudPlatform/terraformer", "ar2_GoogleCloudPlatform_2fterraformer_2faws_0.8.20", false},
		{"GoogleCloudPlatform/terraformer/aws", "ar2_GoogleCloudPlatform_2fterraformer_2faws_0.8.20", true},
		// A version can hold a slash of its own, which encodes the same way a package's does.
		{"kubernetes-sigs/kustomize", "ar2_kubernetes-sigs_2fkustomize_kustomize_2fv5.8.1", true},
		// The package's own branch carries no version.
		{"cli/cli", "ar2_cli_2fcli", false},
		{"cli/cli", "ar2_cli_2fcli_", false},
		{"cli/cli", "ar2_cli_2fcli_v2.1.0", true},
		{"cli/cli", "pkg_cli_2fcli", false},
	}
	for _, d := range data {
		t.Run(d.pkg+" "+d.branch, func(t *testing.T) {
			t.Parallel()
			if got := g2.IsVersionHeadBranch(d.pkg, d.branch); got != d.exp {
				t.Fatalf("got %v, want %v", got, d.exp)
			}
		})
	}
}
