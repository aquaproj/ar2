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
	"github.com/aquaproj/aqua/v2/pkg/ghattestation"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/suzuki-shunsuke/go-osenv/osenv"
)

// ghPath finds the GitHub CLI that checks attestations.
//
// It is the copy aqua installs for the same purpose, found the same way aqua finds
// it, so a run uses one gh rather than two. One already on PATH will do when the
// verifiers haven't installed theirs yet.
func ghPath(ctx context.Context) string {
	rt := runtime.NewR(ctx)
	pkg := ghattestation.Package()
	pkg.PackageInfo.OverrideByRuntime(rt)
	if files := pkg.PackageInfo.GetFiles(); len(files) > 0 {
		if p, err := pkg.ExePath(config.GetRootDir(osenv.New(), ""), files[0], rt); err == nil {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	return ""
}

// verification is the part of "gh attestation verify --format json" that says who
// signed.
type verification []struct {
	VerificationResult struct {
		Signature struct {
			Certificate struct {
				SubjectAlternativeName string `json:"subjectAlternativeName"`
				BuildSignerURI         string `json:"buildSignerURI"`
				SourceRepositoryURI    string `json:"sourceRepositoryURI"`
			} `json:"certificate"`
		} `json:"signature"`
	} `json:"verificationResult"`
}

// attestationSigner verifies the artifact's attestation and returns the workflow
// that signed it, in the form a registry names one.
//
// The verification and the reading are one command. Asking who signed costs about
// ten seconds, which is worth paying once for a package being taken over and not
// worth paying again for every version after it.
func (v *Verifier) attestationSigner(ctx context.Context, repo, path string) (string, error) {
	if v.gh == "" {
		return "", errNoGH
	}
	owner, _, _ := strings.Cut(repo, "/")
	// The owner rather than the repository, because naming the repository makes gh
	// expect the workflow that signed to be in it. A release built by a reusable
	// workflow somewhere else then fails, which is the very case the signer has to
	// be read for: taiki-e/cargo-llvm-cov is signed by a workflow in
	// taiki-e/github-actions. What naming the repository would have checked is
	// checked below instead, against what the attestation says it was built from.
	//
	// The command is the GitHub CLI aqua installs, and its arguments are a file ar2
	// downloaded and an owner from the package's own definition.
	cmd := exec.CommandContext(ctx, v.gh, "attestation", "verify", path, "--owner", owner, "--format", "json") //nolint:gosec // see above
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
			return "", fmt.Errorf("verify the attestation: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("verify the attestation: %w", err)
	}

	var res verification
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("read the verification as JSON: %w", err)
	}
	if len(res) == 0 {
		return "", errNoAttestation
	}
	cert := res[0].VerificationResult.Signature.Certificate
	if want := "https://github.com/" + repo; cert.SourceRepositoryURI != want {
		return "", fmt.Errorf("%w: built from %s, not %s", errWrongSource, cert.SourceRepositoryURI, want)
	}
	uri := cert.SubjectAlternativeName
	if uri == "" {
		uri = cert.BuildSignerURI
	}
	signer := signerWorkflow(uri)
	if signer == "" {
		return "", errNoIdentity
	}
	return signer, nil
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
