// Package sign checks that an asset carries the signatures its entry says it does.
//
// Recording a signature is not the same as there being one. The entry says how the
// asset can be verified; this runs the verification, with aqua's own verifiers, so
// that what aqua-registry-g2 promises is what aqua will be able to do at install
// time. A release that is signed for the generator and not for the installer would
// be the worst of both.
package sign

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/aquaproj/aqua/v2/pkg/config"
	"github.com/aquaproj/aqua/v2/pkg/config/aqua"
	"github.com/aquaproj/aqua/v2/pkg/cosign"
	"github.com/aquaproj/aqua/v2/pkg/download"
	"github.com/aquaproj/aqua/v2/pkg/ghattestation"
	aquagithub "github.com/aquaproj/aqua/v2/pkg/github"
	"github.com/aquaproj/aqua/v2/pkg/lockfile"
	"github.com/aquaproj/aqua/v2/pkg/minisign"
	"github.com/aquaproj/aqua/v2/pkg/osexec"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/aqua/v2/pkg/slsa"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/suzuki-shunsuke/go-osenv/osenv"
)

// Verifier runs the signature verifications an entry asks for.
type Verifier struct {
	httpClient *http.Client
	// gh is the GitHub CLI that checks attestations, or empty when there is none
	// to run.
	gh            string
	cosign        *cosign.Verifier
	slsa          *slsa.Verifier
	minisign      *minisign.Verifier
	ghattestation *ghattestation.Verifier
}

// New creates a Verifier.
//
// The verification tools are installed on demand by the verifiers themselves, once
// per run, into aqua's own root directory — the same copies aqua would use, and the
// same place AQUA_ROOT_DIR points at.
func New(ctx context.Context, logger *slog.Logger, httpClient *http.Client) (*Verifier, error) {
	param := &config.Param{RootDir: config.GetRootDir(osenv.New())}
	exe := osexec.New()
	// aqua's own client, because the downloader is aqua's: it reads the token from
	// the same environment variable ar2 does.
	gh, err := aquagithub.New(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("create a GitHub client: %w", err)
	}
	dl := download.NewDownloader(gh, download.NewHTTPDownloader(logger, httpClient))

	minisignExe, err := minisign.NewExecutor(logger, exe, param)
	if err != nil {
		return nil, fmt.Errorf("prepare minisign: %w", err)
	}
	attestExe, err := ghattestation.NewExecutor(exe, param)
	if err != nil {
		return nil, fmt.Errorf("prepare gh: %w", err)
	}
	return &Verifier{
		httpClient:    httpClient,
		gh:            ghPath(ctx),
		cosign:        cosign.NewVerifier(exe, dl, param),
		slsa:          slsa.New(dl, slsa.NewExecutor(exe, param)),
		minisign:      minisign.New(dl, minisignExe),
		ghattestation: ghattestation.New(attestExe),
	}, nil
}

// Check verifies every signature the asset's entry carries, against the file at
// path, and drops the ones that don't hold. It returns what it dropped.
//
// Dropping rather than failing, because a signature that doesn't verify is a
// statement the entry can't make. Some of them are read off the release — a
// .sigstore.json beside an asset means it is signed, but not by whom, so the signer
// is guessed at as a workflow in the package's own repository. sigstore/cosign signs
// its releases as keyless@projectsigstore.iam.gserviceaccount.com, and an entry
// carrying that guess would fail for every user who installed it.
//
// What is dropped is not nothing, though: the caller leaves the version for review,
// and the comparison against the version before it reports the loss. A release that
// really did stop being signed and a guess that was wrong look the same from here,
// and both are for a person to settle.
//
// The environment the asset is for is what the signature names are rendered with,
// not the machine this runs on: the entry for darwin/arm64 is verified with
// darwin/arm64's signature wherever the run happens.
func (v *Verifier) Check(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, path string) []string {
	rt := &runtime.Runtime{GOOS: asset.OS, GOARCH: asset.Arch}
	pkg := assetPackage(pkgName, version, asset)
	art := pkg.TemplateArtifact(rt, asset.Asset)
	file := &download.File{
		RepoOwner: asset.RepoOwner,
		RepoName:  asset.RepoName,
		Version:   version,
	}

	var dropped []string
	drop := func(kind string, err error) {
		logger.Warn("the asset can't be verified the way its entry says it can",
			"package", pkgName, "version", version,
			"os", asset.OS, "arch", asset.Arch, "kind", kind, "error", err.Error())
		dropped = append(dropped, kind)
	}

	// Who signed is read off the signature before it is checked, so that a guessed
	// pattern is replaced by a name and the entry records what actually signed.
	v.pin(ctx, logger, version, asset)

	if asset.Cosign.GetEnabled() {
		if err := v.cosign.Verify(ctx, logger, rt, file, asset.Cosign, art, path); err != nil {
			asset.Cosign = nil
			drop("cosign", err)
		}
	}
	if asset.SLSAProvenance.GetEnabled() {
		if err := v.slsa.Verify(ctx, logger, rt, asset.SLSAProvenance, art, file, &slsa.ParamVerify{
			SourceURI:    pkg.PackageInfo.SLSASourceURI(),
			SourceTag:    version,
			ArtifactPath: path,
		}); err != nil {
			asset.SLSAProvenance = nil
			drop("slsa_provenance", err)
		}
	}
	if asset.Minisign.GetEnabled() {
		if err := v.minisign.Verify(ctx, logger, rt, asset.Minisign, art, file, &minisign.ParamVerify{
			ArtifactPath: path,
			PublicKey:    asset.Minisign.PublicKey,
		}); err != nil {
			asset.Minisign = nil
			drop("minisign", err)
		}
	}
	if asset.GitHubArtifactAttestations.GetEnabled() {
		if err := v.attestation(ctx, logger, asset, path); err != nil {
			asset.GitHubArtifactAttestations = nil
			drop("github_artifact_attestations", err)
		}
	}
	return dropped
}

// assetPackage turns the entry back into the package aqua would install, which is
// what the verifiers render their template values from.
func assetPackage(pkgName, version string, asset *generate.Asset) *config.Package {
	entry := &lockfile.Package{
		Name:                       pkgName,
		Version:                    version,
		OS:                         asset.OS,
		Arch:                       asset.Arch,
		Type:                       asset.Type,
		RepoOwner:                  asset.RepoOwner,
		RepoName:                   asset.RepoName,
		Asset:                      asset.Asset,
		URL:                        asset.URL,
		Format:                     asset.Format,
		Cosign:                     asset.Cosign,
		GitHubArtifactAttestations: asset.GitHubArtifactAttestations,
		Minisign:                   asset.Minisign,
		SLSAProvenance:             asset.SLSAProvenance,
	}
	return &config.Package{
		Package:     &aqua.Package{Name: pkgName, Version: version},
		PackageInfo: entry.PackageInfo(),
	}
}

// attestation checks the artifact's attestation, and records which workflow signed
// it when the definition doesn't say.
//
// Both are one run of the GitHub CLI. Asking who signed costs about ten seconds, so
// it is paid once for a package being taken over rather than for every version after
// it; once the definition names the workflow, the check is made against that name.
func (v *Verifier) attestation(ctx context.Context, logger *slog.Logger, asset *generate.Asset, path string) error {
	repo := asset.RepoOwner + "/" + asset.RepoName
	if asset.GitHubArtifactAttestations.SignerWorkflow() != "" {
		return v.ghattestation.Verify(ctx, logger, &ghattestation.ParamVerify{ //nolint:wrapcheck // the caller says what it was checking
			ArtifactPath:   path,
			Repository:     repo,
			SignerWorkflow: asset.GitHubArtifactAttestations.SignerWorkflow(),
			PredicateType:  asset.GitHubArtifactAttestations.PredicateType,
		})
	}

	signer, err := v.attestationSigner(ctx, repo, path)
	if err != nil {
		return err
	}
	logger.Debug("read which workflow signed the attestation",
		"os", asset.OS, "arch", asset.Arch, "signer_workflow", signer)
	asset.GitHubArtifactAttestations.SignerWorkflow2 = signer
	return nil
}
