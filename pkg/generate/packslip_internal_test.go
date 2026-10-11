package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// What packslip.sigstore.json said for jdx/hk v2.5.0.
func hkSigner() *signer {
	return &signer{
		Identity: "https://github.com/jdx/hk/.github/workflows/release.yml@refs/tags/v2.5.0",
		Issuer:   "https://token.actions.githubusercontent.com",
	}
}

func TestHeldToPackslip(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		in     *aquaregistry.Packslip
		signer *signer
		want   *aquaregistry.Packslip
	}{
		{
			name:   "the bundle's signer replaces aqua gr's prefix",
			in:     &aquaregistry.Packslip{IdentityPrefix: "https://github.com/jdx/hk/", Issuer: "https://token.actions.githubusercontent.com"},
			signer: hkSigner(),
			want:   &aquaregistry.Packslip{Identity: hkSigner().Identity, Issuer: hkSigner().Issuer},
		},
		{
			name:   "a definition naming whom to trust wins",
			in:     &aquaregistry.Packslip{Pin: "ps1_snirenkjwr7m5ozgcufameodnm"},
			signer: hkSigner(),
			want:   &aquaregistry.Packslip{Pin: "ps1_snirenkjwr7m5ozgcufameodnm"},
		},
		{
			name: "a bundle that couldn't be read leaves the repository's prefix",
			in:   &aquaregistry.Packslip{},
			want: &aquaregistry.Packslip{IdentityPrefix: "https://github.com/jdx/hk/", Issuer: "https://token.actions.githubusercontent.com"},
		},
		{
			name:   "a bundle signed with a key, and nothing naming the key, is no check",
			in:     &aquaregistry.Packslip{},
			signer: &signer{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := heldToPackslip(discardLogger(), tt.in, tt.signer, "jdx", "hk")
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// The bundle is the release's own, so only entries downloading from that release are
// covered by it.
func TestInRelease(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		asset *Asset
		want  bool
	}{
		{&Asset{Type: "github_release", Asset: "hk.tar.gz"}, true},
		{&Asset{Type: "github_release", RepoOwner: "JDX", RepoName: "hk", Asset: "hk.tar.gz"}, true},
		{&Asset{Type: "github_release", RepoOwner: "other", RepoName: "repo", Asset: "x.tar.gz"}, false},
		{&Asset{Type: "http", URL: "https://example.com/hk.tar.gz"}, false},
		{&Asset{Type: "forgejo_release", Host: "codeberg.org", Asset: "hk.tar.gz"}, false},
	} {
		if got := inRelease(tt.asset, "jdx", "hk"); got != tt.want {
			t.Errorf("inRelease(%+v) = %v, want %v", tt.asset, got, tt.want)
		}
	}
}

// A release that doesn't carry the bundle loses the packslip the definition gives it: the
// versions before a project started publishing one have nothing to verify against.
func TestDropUnpublished_packslip(t *testing.T) {
	t.Parallel()
	reg := &Registry{Assets: []*Asset{{Type: "github_release", Asset: "hk.tar.gz", Packslip: &aquaregistry.Packslip{IdentityPrefix: "https://github.com/jdx/hk/"}}}}
	dropUnpublished(discardLogger(), reg, map[string]struct{}{"hk.tar.gz": {}})
	if reg.Assets[0].Packslip != nil {
		t.Error("packslip was kept for a release without the bundle")
	}
	reg.Assets[0].Packslip = &aquaregistry.Packslip{IdentityPrefix: "https://github.com/jdx/hk/"}
	dropUnpublished(discardLogger(), reg, map[string]struct{}{"hk.tar.gz": {}, aquaregistry.BundleAssetPackslip: {}})
	if reg.Assets[0].Packslip == nil {
		t.Error("packslip was dropped although the release carries the bundle")
	}
}
