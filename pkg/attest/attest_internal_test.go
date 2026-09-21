package attest

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/szksh-lab-2/ar2/pkg/generate"
)

// fakeAttestations answers for the digests it was given and records what was asked.
type fakeAttestations struct {
	attested map[string]bool
	asked    []string
	notFound bool
}

func (f *fakeAttestations) ListAttestations(_ context.Context, _, _, subjectDigest string, _ *gogithub.ListOptions) (*gogithub.AttestationsResponse, *gogithub.Response, error) {
	f.asked = append(f.asked, subjectDigest)
	if f.notFound {
		return nil, &gogithub.Response{Response: &http.Response{StatusCode: http.StatusNotFound}},
			&gogithub.ErrorResponse{Message: "Not Found"}
	}
	res := &gogithub.AttestationsResponse{}
	if f.attested[subjectDigest] {
		res.Attestations = []*gogithub.Attestation{{}}
	}
	return res, &gogithub.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
}

func logger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func attestedAsset(os, arch, checksum string) *generate.Asset {
	return &aquag2.Asset{
		OS: os, Arch: arch, Checksum: checksum,
		GitHubArtifactAttestations: &aquaregistry.GitHubArtifactAttestations{
			SignerWorkflow2: "cli/cli/.github/workflows/deployment.yml",
		},
	}
}

func TestChecker_Check(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*generate.Asset{
		attestedAsset("linux", "amd64", "aa"),
		attestedAsset("darwin", "arm64", "bb"),
	}}
	gh := &fakeAttestations{attested: map[string]bool{"sha256:aa": true}}

	dropped, err := New(gh).Check(t.Context(), logger(), "cli/cli", reg)
	if err != nil {
		t.Fatal(err)
	}
	if !dropped {
		t.Error("an asset lost its attestation, which the caller has to know about")
	}
	if reg.Assets[0].GitHubArtifactAttestations == nil {
		t.Error("the attested asset kept nothing")
	}
	if reg.Assets[1].GitHubArtifactAttestations != nil {
		t.Error("the asset GitHub holds no attestation for still claims one")
	}
}

// A repository that has never attested anything answers 404, which means the same
// as an empty list.
func TestChecker_Check_notFound(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*generate.Asset{attestedAsset("linux", "amd64", "aa")}}
	dropped, err := New(&fakeAttestations{notFound: true}).Check(t.Context(), logger(), "cli/cli", reg)
	if err != nil {
		t.Fatal(err)
	}
	if !dropped || reg.Assets[0].GitHubArtifactAttestations != nil {
		t.Error("an attestation GitHub doesn't have must be dropped")
	}
}

// A lookup is a request per asset. The packages whose definition claims no
// attestation must not pay for one.
func TestChecker_Check_onlyWhatClaimsOne(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*generate.Asset{
		{OS: "linux", Arch: "amd64", Checksum: "aa"},
		// Claims one, but nothing has hashed it yet, so there is nothing to ask
		// about.
		{OS: "darwin", Arch: "arm64", GitHubArtifactAttestations: &aquaregistry.GitHubArtifactAttestations{}},
	}}
	gh := &fakeAttestations{}
	dropped, err := New(gh).Check(t.Context(), logger(), "cli/cli", reg)
	if err != nil {
		t.Fatal(err)
	}
	if dropped {
		t.Error("nothing was dropped")
	}
	if len(gh.asked) != 0 {
		t.Errorf("asked about %v, want nothing", gh.asked)
	}
}
