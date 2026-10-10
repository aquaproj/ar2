package sign

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/google/go-cmp/cmp"
)

// What gh printed for cli/cli v2.99.0's linux/amd64 archive.
func cliCertificate() *attestationCertificate {
	return &attestationCertificate{
		SubjectAlternativeName: "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
		Issuer:                 "https://token.actions.githubusercontent.com",
		BuildSignerURI:         "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
		RunnerEnvironment:      "github-hosted",
		SourceRepositoryURI:    "https://github.com/cli/cli",
		SourceRepositoryDigest: "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0",
		SourceRepositoryRef:    "refs/heads/trunk",
	}
}

// The certificate's ref, commit, issuer and runner go onto the entry, and the workflow's
// repository only when it isn't the artifact's own.
func TestAttestationCertificate_pin(t *testing.T) {
	t.Parallel()
	gaa := &aquaregistry.GitHubArtifactAttestations{SignerWorkflow2: "cli/cli/.github/workflows/deployment.yml"}
	cliCertificate().pin(gaa, "cli/cli")
	want := &aquaregistry.GitHubArtifactAttestations{
		SignerWorkflow2:       "cli/cli/.github/workflows/deployment.yml",
		SourceRef:             "refs/heads/trunk",
		SourceDigest:          "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0",
		CertOIDCIssuer:        "https://token.actions.githubusercontent.com",
		DenySelfHostedRunners: true,
	}
	if diff := cmp.Diff(want, gaa); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	reusable := cliCertificate()
	reusable.SubjectAlternativeName = "https://github.com/taiki-e/github-actions/.github/workflows/rust-release.yml@refs/heads/main"
	reusable.RunnerEnvironment = "self-hosted"
	other := &aquaregistry.GitHubArtifactAttestations{}
	reusable.pin(other, "cli/cli")
	if other.SignerRepo != "taiki-e/github-actions" || other.DenySelfHostedRunners {
		t.Errorf("a reusable workflow on a self-hosted runner: %+v", other)
	}
}

// An entry is verified against everything it pins, in a fixed order.
func TestAttestationArgs(t *testing.T) {
	t.Parallel()
	gaa := &aquaregistry.GitHubArtifactAttestations{
		SignerWorkflow2:       `cli/cli/\.github/workflows/deployment\.yml`,
		SourceRef:             "refs/heads/trunk",
		SourceDigest:          "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0",
		CertOIDCIssuer:        "https://token.actions.githubusercontent.com",
		DenySelfHostedRunners: true,
	}
	want := []string{
		"--signer-workflow", "cli/cli/.github/workflows/deployment.yml",
		"--source-ref", "refs/heads/trunk",
		"--source-digest", "d528f20f2ee02f6703773e9f56c90e3c3f5d46b0",
		"--cert-oidc-issuer", "https://token.actions.githubusercontent.com",
		"--deny-self-hosted-runners",
	}
	if diff := cmp.Diff(want, attestationArgs(gaa)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
