package sign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/config"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/ghattestation"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/suzuki-shunsuke/go-osenv/osenv"
)

// ghPath finds the GitHub CLI that checks attestations.
//
// aqua is asked where it puts the one it installs, so that a run uses the copy it
// just installed rather than a second one. A gh already on PATH answers for an
// environment aqua's package doesn't cover, where installing it was never going to
// work.
func ghPath(ctx context.Context) string {
	p, err := ghattestation.ExePath(&ghattestation.ParamExePath{
		RootDir: config.GetRootDir(osenv.New(), ""),
		Runtime: runtime.NewR(ctx),
	})
	if err == nil {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	return ""
}

// verification is the part of "gh attestation verify --format json" that says what the
// signing certificate records about the run that built the artifact.
type verification []struct {
	VerificationResult struct {
		Signature struct {
			Certificate *attestationCertificate `json:"certificate"`
		} `json:"signature"`
	} `json:"verificationResult"`
}

// attestationCertificate is what Fulcio recorded in the certificate an attestation was
// signed with, as gh prints it.
type attestationCertificate struct {
	SubjectAlternativeName string `json:"subjectAlternativeName"`
	Issuer                 string `json:"issuer"`
	BuildSignerURI         string `json:"buildSignerURI"`
	RunnerEnvironment      string `json:"runnerEnvironment"`
	SourceRepositoryURI    string `json:"sourceRepositoryURI"`
	SourceRepositoryDigest string `json:"sourceRepositoryDigest"`
	SourceRepositoryRef    string `json:"sourceRepositoryRef"`
}

// signer is the URI of the workflow that signed.
func (c *attestationCertificate) signer() string {
	if c.SubjectAlternativeName != "" {
		return c.SubjectAlternativeName
	}
	return c.BuildSignerURI
}

// pin records on the entry what the certificate says, which aqua passes to gh attestation
// verify: the ref and the commit the artifact was built from pin the version to that build
// rather than only to the workflow. The repository of the workflow that signed is recorded
// only when it isn't the artifact's own -- a reusable workflow -- since the workflow's path
// says it otherwise.
func (c *attestationCertificate) pin(gaa *aquaregistry.GitHubArtifactAttestations, repo string) {
	gaa.SourceRef = c.SourceRepositoryRef
	gaa.SourceDigest = c.SourceRepositoryDigest
	gaa.CertOIDCIssuer = c.Issuer
	gaa.DenySelfHostedRunners = c.RunnerEnvironment == "github-hosted"
	gaa.SignerRepo = ""
	if signer := repositoryOfWorkflow(signerWorkflow(c.signer())); signer != "" && !strings.EqualFold(signer, repo) {
		gaa.SignerRepo = signer
	}
}

// repositoryOfWorkflow is owner/name out of a workflow path such as
// owner/name/.github/workflows/release.yml.
func repositoryOfWorkflow(workflow string) string {
	parts := strings.SplitN(workflow, "/", 3) //nolint:mnd
	if len(parts) < 3 {                       //nolint:mnd
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// verifyAttestation verifies the artifact's attestation and returns the certificate it was
// signed with.
//
// The owner is named rather than the repository, because naming the repository makes gh
// expect the workflow that signed to be in it. A release built by a reusable workflow
// somewhere else then fails: taiki-e/cargo-llvm-cov is signed by a workflow in
// taiki-e/github-actions. What naming the repository would have checked is checked here
// instead, against what the attestation says it was built from.
//
// Whatever the entry already says is passed too, so an entry is verified against everything
// it pins.
func (v *Verifier) verifyAttestation(ctx context.Context, repo, path string, gaa *aquaregistry.GitHubArtifactAttestations) (*attestationCertificate, error) {
	gh := v.ghExe(ctx)
	if gh == "" {
		return nil, errNoGH
	}
	owner, _, _ := strings.Cut(repo, "/")
	args := append([]string{"attestation", "verify", path, "--owner", owner, "--format", "json"},
		attestationArgs(gaa)...)
	// The command is the GitHub CLI aqua installs, and its arguments are a file ar2
	// downloaded and values from the package's own definition and entry.
	cmd := exec.CommandContext(ctx, gh, args...)
	cmd.Args[0] = "gh"
	// Packages are always on github.com, whatever GH_HOST says for the repository
	// the run itself is against.
	cmd.Env = append(os.Environ(), "GH_HOST=github.com")
	out, err := cmd.Output()
	if err != nil {
		// The exit status alone says nothing about why. What gh printed is the
		// difference between "this release isn't attested" and "the token can't
		// read it", which are acted on differently.
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("verify the attestation: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("verify the attestation: %w", err)
	}

	var res verification
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("read the verification as JSON: %w", err)
	}
	if len(res) == 0 || res[0].VerificationResult.Signature.Certificate == nil {
		return nil, errNoAttestation
	}
	cert := res[0].VerificationResult.Signature.Certificate
	if want := "https://github.com/" + repo; !strings.EqualFold(cert.SourceRepositoryURI, want) {
		return nil, fmt.Errorf("%w: built from %s, not %s", errWrongSource, cert.SourceRepositoryURI, want)
	}
	return cert, nil
}

// attestationArgs are the flags holding the attestation to what the entry says.
func attestationArgs(gaa *aquaregistry.GitHubArtifactAttestations) []string {
	if gaa == nil {
		return nil
	}
	fields := []struct{ flag, value string }{
		{"--signer-workflow", unescape(gaa.SignerWorkflow())},
		{"--predicate-type", gaa.PredicateType},
		{"--signer-repo", gaa.SignerRepo},
		{"--source-ref", gaa.SourceRef},
		{"--source-digest", gaa.SourceDigest},
		{"--cert-oidc-issuer", gaa.CertOIDCIssuer},
	}
	args := []string{}
	for _, f := range fields {
		if f.value != "" {
			args = append(args, f.flag, f.value)
		}
	}
	if gaa.DenySelfHostedRunners {
		args = append(args, "--deny-self-hosted-runners")
	}
	return args
}

// unescape undoes the escaping registries written for older gh put on a workflow's dots,
// which gh now matches literally.
func unescape(workflow string) string {
	return strings.ReplaceAll(workflow, `\.`, ".")
}

// signerWorkflow turns the URI a workflow signs under into the path a registry names
// it by.
//
// The certificate says which ref the workflow ran for, which a registry doesn't:
// releases are signed by the same workflow from different tags, and naming the ref
// would pin the package to one of them.
//
//	https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk
//	cli/cli/.github/workflows/deployment.yml
func signerWorkflow(uri string) string {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	path := strings.TrimPrefix(uri, prefix)
	if i := strings.Index(path, "@"); i >= 0 {
		path = path[:i]
	}
	return path
}
