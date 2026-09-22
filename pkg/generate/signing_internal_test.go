package generate

import (
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

func names(n ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(n))
	for _, s := range n {
		m[s] = struct{}{}
	}
	return m
}

// An asset with a bundle beside it is signed, and the identity is held to the
// package's own repository rather than to any signer at all.
func TestInferSigning(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64"},
		{OS: "linux", Arch: "amd64", Asset: "cosign-linux-amd64"},
	}}
	err := inferSigning("sigstore/cosign", reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json", "cosign-linux-amd64"))
	if err != nil {
		t.Fatal(err)
	}

	got := reg.Assets[0].Cosign
	if got == nil {
		t.Fatal("the asset with a bundle beside it must be signed")
	}
	if *got.Bundle.Asset != "cosign-darwin-amd64.sigstore.json" {
		t.Errorf("the bundle is %q", *got.Bundle.Asset)
	}
	if !strings.Contains(strings.Join(got.Opts, " "), "sigstore/cosign") {
		t.Errorf("the identity isn't held to the repository: %v", got.Opts)
	}
	if reg.Assets[1].Cosign != nil {
		t.Errorf("the asset with nothing beside it must not be signed: %+v", reg.Assets[1].Cosign)
	}
}

// A definition that says how the asset is signed was written by someone who knows
// the release. The inference has to generalize about the signer and doesn't get to
// overrule it.
func TestInferSigning_definitionWins(t *testing.T) {
	t.Parallel()
	declared := &aquaregistry.Cosign{Opts: []string{"--certificate-identity", "exactly this one"}}
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{OS: "darwin", Arch: "amd64", Asset: "cosign-darwin-amd64", Cosign: declared},
	}}
	if err := inferSigning("sigstore/cosign", reg,
		names("cosign-darwin-amd64", "cosign-darwin-amd64.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	if reg.Assets[0].Cosign != declared {
		t.Error("the definition's configuration was replaced")
	}
}
