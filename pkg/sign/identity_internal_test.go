package sign

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The identity is read off the signature rather than guessed at.
//
// sigstore/cosign is the case that made this necessary: aqua gr assumes the signer
// is a workflow in the package's own repository, and a definition carrying that
// assumption fails for everyone who installs it.
func TestIdentityFromBundle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
		want *Identity
	}{
		{
			name: "signed by a workflow",
			file: "testdata/workflow.sigstore.json",
			want: &Identity{
				SAN:        "https://github.com/goreleaser/goreleaser/.github/workflows/release.yml@refs/tags/v2.18.2",
				Issuer:     "https://token.actions.githubusercontent.com",
				Repository: "goreleaser/goreleaser",
				Ref:        "refs/tags/v2.18.2",
			},
		},
		{
			// The guess would have been a workflow in sigstore/cosign.
			name: "signed by a service account",
			file: "testdata/service-account.sigstore.json",
			// A service account belongs to no repository and ran for no ref, so
			// there is nothing more to ask cosign for.
			want: &Identity{
				SAN:    "keyless@projectsigstore.iam.gserviceaccount.com",
				Issuer: "https://accounts.google.com",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := identityFromBundle(b)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the identity is wrong (-want +got):\n%s", diff)
			}
		})
	}
}

// The flags are what cosign is given to check for exactly this signer, rather than
// for anything matching a pattern.
func TestIdentity_Opts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   *Identity
		want []string
	}{
		{
			name: "a service account",
			id:   &Identity{SAN: "keyless@projectsigstore.iam.gserviceaccount.com", Issuer: "https://accounts.google.com"},
			want: []string{
				"--certificate-identity", "keyless@projectsigstore.iam.gserviceaccount.com",
				"--certificate-oidc-issuer", "https://accounts.google.com",
			},
		},
		{
			// The workflow is shared, so the identity alone would accept a
			// certificate obtained for somebody else's release.
			name: "a workflow another repository also uses",
			id: &Identity{
				SAN:        "https://github.com/suzuki-shunsuke/go-release-workflow/.github/workflows/release.yaml@1cf29d7b17983b901021799b87a55d357cfff790",
				Issuer:     "https://token.actions.githubusercontent.com",
				Repository: "aquaproj/ar2",
				Ref:        "refs/tags/v0.0.7",
			},
			want: []string{
				"--certificate-identity", "https://github.com/suzuki-shunsuke/go-release-workflow/.github/workflows/release.yaml@1cf29d7b17983b901021799b87a55d357cfff790",
				"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
				"--certificate-github-workflow-repository", "aquaproj/ar2",
				"--certificate-github-workflow-ref", "refs/tags/v0.0.7",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, tt.id.Opts()); diff != "" {
				t.Errorf("the options are wrong (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIdentityFromBundle_noCertificate(t *testing.T) {
	t.Parallel()
	if _, err := identityFromBundle([]byte(`{"verificationMaterial":{}}`)); err == nil {
		t.Fatal("a bundle with no certificate must be an error")
	}
}
