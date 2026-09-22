package sign

import "testing"

// A registry names the workflow by its path, not by the ref it ran for. Releases are
// signed by the same workflow from different tags, and naming the ref would pin the
// package to one of them.
func TestSignerWorkflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "signed from a branch",
			uri:  "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
			want: "cli/cli/.github/workflows/deployment.yml",
		},
		{
			name: "signed from a tag",
			uri:  "https://github.com/suzuki-shunsuke/tfcmt/.github/workflows/release.yaml@refs/tags/v4.14.2",
			want: "suzuki-shunsuke/tfcmt/.github/workflows/release.yaml",
		},
		{
			name: "no ref at all",
			uri:  "https://github.com/cli/cli/.github/workflows/deployment.yml",
			want: "cli/cli/.github/workflows/deployment.yml",
		},
		{
			// Something that isn't a GitHub workflow has no workflow to name.
			name: "not a workflow",
			uri:  "keyless@projectsigstore.iam.gserviceaccount.com",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := signerWorkflow(tt.uri); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
