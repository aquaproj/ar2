package generate

import (
	"encoding/base64"
	"os"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// envelope is a bare DSSE envelope of a statement naming the builder.
func envelope(builder string) []byte {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"predicate":{"builder":{"id":"` + builder + `"}}}`))
	return []byte(`{"payloadType":"application/vnd.in-toto+json","payload":"` + payload + `","signatures":[]}` + "\n")
}

// A provenance is read for who built it and who signed it.
func TestReadProvenance(t *testing.T) {
	t.Parallel()
	attestation, err := os.ReadFile("testdata/aidlc-release.intoto.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		name string
		file []byte
		want provenanceInfo
	}{
		{
			name: "slsa-github-generator",
			file: envelope("https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0"),
			want: provenanceInfo{kind: provenanceSLSA},
		},
		{
			// awslabs/aidlc-workflows' release workflow attests its own build.
			name: "a bundle signed with an identity",
			file: attestation,
			want: provenanceInfo{kind: provenanceBundle, signer: &signer{
				Identity: "https://github.com/awslabs/aidlc-workflows/.github/workflows/release.yml@refs/tags/v2.11.0",
				Issuer:   githubOIDCIssuer,
			}},
		},
		{
			name: "a builder nothing verifies",
			file: envelope("https://example.com/builder"),
			want: provenanceInfo{kind: provenanceNone},
		},
		{
			// Unreadable, it is taken for what every provenance was taken for before.
			name: "not JSON",
			file: []byte("not a provenance"),
			want: provenanceInfo{kind: provenanceSLSA},
		},
	} {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(d.want, readProvenance(d.file), cmp.AllowUnexported(provenanceInfo{}, signer{})); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// A bundle becomes the entry's cosign, held to who signed it, rather than its
// slsa_provenance, and a provenance nothing verifies becomes neither.
func TestInferSLSA_kinds(t *testing.T) {
	t.Parallel()
	reg := &Registry{Assets: []*Asset{
		{OS: "darwin", Arch: "amd64", Type: "github_release", Asset: "aidlc-darwin-x64"},
	}}
	identity := "https://github.com/awslabs/aidlc-workflows/.github/workflows/release.yml@refs/tags/v2.11.0"
	inferSLSA(reg, nil, names("aidlc-darwin-x64", "aidlc-release.intoto.jsonl"), func(string) provenanceInfo {
		return provenanceInfo{kind: provenanceBundle, signer: &signer{Identity: identity, Issuer: githubOIDCIssuer}}
	})
	bundle := "aidlc-release.intoto.jsonl"
	want := &aquaregistry.Cosign{
		Bundle: &aquaregistry.DownloadedFile{Type: "github_release", Asset: &bundle},
		Opts:   []string{"--certificate-identity", identity, "--certificate-oidc-issuer", githubOIDCIssuer},
	}
	if diff := cmp.Diff(want, reg.Assets[0].Cosign); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if reg.Assets[0].SLSAProvenance != nil {
		t.Error("a bundle was taken for an SLSA provenance")
	}

	reg = &Registry{Assets: []*Asset{{OS: "darwin", Arch: "amd64", Type: "github_release", Asset: "foo"}}}
	inferSLSA(reg, nil, names("foo", "multiple.intoto.jsonl"), func(string) provenanceInfo { return provenanceInfo{kind: provenanceNone} })
	if reg.Assets[0].SLSAProvenance != nil || reg.Assets[0].Cosign != nil {
		t.Error("a provenance nothing verifies was given a way to verify it")
	}
}
