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
	"sync"

	"github.com/aquaproj/aqua/v2/pkg/checksum"
	"github.com/aquaproj/aqua/v2/pkg/config"
	"github.com/aquaproj/aqua/v2/pkg/config/aqua"
	"github.com/aquaproj/aqua/v2/pkg/cosign"
	"github.com/aquaproj/aqua/v2/pkg/download"
	"github.com/aquaproj/aqua/v2/pkg/ghattestation"
	aquagithub "github.com/aquaproj/aqua/v2/pkg/github"
	"github.com/aquaproj/aqua/v2/pkg/installpackage"
	"github.com/aquaproj/aqua/v2/pkg/link"
	"github.com/aquaproj/aqua/v2/pkg/lockfile"
	"github.com/aquaproj/aqua/v2/pkg/minisign"
	"github.com/aquaproj/aqua/v2/pkg/osexec"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/aqua/v2/pkg/slsa"
	"github.com/aquaproj/aqua/v2/pkg/unarchive"
	"github.com/aquaproj/aqua/v2/pkg/vacuum"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/suzuki-shunsuke/go-osenv/osenv"
)

// Verifier runs the signature verifications an entry asks for.
type Verifier struct {
	httpClient *http.Client
	// gh is the GitHub CLI that checks attestations, or empty when there is none to
	// run. It is looked for on first use rather than at construction, because the
	// copy it should prefer is the one installed for the check about to be made.
	gh            string
	ghOnce        sync.Once
	cosign        *cosign.Verifier
	slsa          *slsa.Verifier
	minisign      *minisign.Verifier
	ghattestation *ghattestation.Verifier

	// installer puts the tools where the verifiers look for them, and rt is the
	// host they are chosen for.
	installer *installpackage.Installer
	rt        *runtime.Runtime
	tools     map[string]*tool
}

// New creates a Verifier.
//
// The verification tools are installed the first time a check needs one, into aqua's
// own root directory — the same copies aqua would use, and the same place
// AQUA_ROOT_DIR points at. aqua's verifiers don't install anything themselves: they
// work out where the tool is and run it, and putting it there is the install path's
// job, which this stands in for.
func New(ctx context.Context, logger *slog.Logger, httpClient *http.Client) (*Verifier, error) {
	param := &config.Param{RootDir: config.GetRootDir(osenv.New(), "")}
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
	cosignVerifier := cosign.NewVerifier(exe, dl, param)
	slsaVerifier := slsa.New(dl, slsa.NewExecutor(exe, param))
	minisignVerifier := minisign.New(dl, minisignExe)
	attestVerifier := ghattestation.New(attestExe)

	rt := runtime.NewR(ctx)
	httpDL := download.NewHTTPDownloader(logger, httpClient)
	return &Verifier{
		httpClient:    httpClient,
		cosign:        cosignVerifier,
		slsa:          slsaVerifier,
		minisign:      minisignVerifier,
		ghattestation: attestVerifier,
		rt:            rt,
		installer: installpackage.New(param, dl, rt, link.New(),
			download.NewChecksumDownloader(gh, rt, httpDL), checksum.NewCalculator(),
			unarchive.New(exe),
			cosignVerifier, slsaVerifier, minisignVerifier, attestVerifier,
			installpackage.NewGoInstallInstallerImpl(exe),
			installpackage.NewGoBuildInstallerImpl(exe),
			installpackage.NewCargoPackageInstallerImpl(exe),
			vacuum.New(param)),
		tools: map[string]*tool{
			toolCosign:   {pkg: cosign.Package, checksums: cosign.Checksums()},
			toolSLSA:     {pkg: slsa.Package, checksums: slsa.Checksums()},
			toolMinisign: {pkg: minisign.Package, checksums: minisign.Checksums()},
			toolGH:       {pkg: ghattestation.Package, checksums: ghattestation.Checksums()},
		},
	}, nil
}

// Check verifies every signature the asset's entry carries against the file at path,
// and reports whether they all held.
//
// A signature that can't be verified stops the version rather than being dropped from
// the entry. The two readings of a failure -- the release stopped being signed, and
// the check couldn't be made right now -- are not distinguishable here, and only one
// of them is safe to act on: recording a signed package as unsigned is a loss that
// nothing later notices, while not publishing a version is undone by the next run.
//
// It happened the other way first. cosign was rate limited while the kubernetes
// packages were generated, five retries inside two seconds all met the same limit,
// and the entries came out saying those releases carry no signature at all.
//
// The cost is a version that stalls: an upstream that really did stop signing, or a
// signer that can't be read off the certificate, produces nothing until somebody
// changes the definition. That is the direction to fail in, and it is why the signer
// is read off the signature first, which settles the case this used to exist for --
// a guessed identity that was never right.
//
// The environment the asset is for is what the signature names are rendered with,
// not the machine this runs on: the entry for darwin/arm64 is verified with
// darwin/arm64's signature wherever the run happens.
func (v *Verifier) Check(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, path string) error {
	rt := &runtime.Runtime{GOOS: asset.OS, GOARCH: asset.Arch}
	pkg := assetPackage(pkgName, version, asset)
	art := pkg.TemplateArtifact(rt, asset.Asset)
	file := &download.File{
		RepoOwner: asset.RepoOwner,
		RepoName:  asset.RepoName,
		Version:   version,
	}

	failed := &unverified{logger: logger, pkgName: pkgName, version: version, asset: asset}

	// Who signed is read off the signature before it is checked, so that a guessed
	// pattern is replaced by a name and the entry records what actually signed.
	v.pin(ctx, logger, version, asset)

	for _, check := range []struct {
		kind string
		// tool is what the check is run with, and what has to be installed before
		// it can be. The attestations are checked by a gh the machine already has.
		tool    string
		enabled bool
		verify  func() error
	}{
		{"cosign", toolCosign, asset.Cosign.GetEnabled(), func() error {
			return v.cosign.Verify(ctx, logger, rt, file, asset.Cosign, art, path)
		}},
		{"slsa_provenance", toolSLSA, asset.SLSAProvenance.GetEnabled(), func() error {
			return v.slsa.Verify(ctx, logger, rt, asset.SLSAProvenance, art, file, &slsa.ParamVerify{
				SourceURI:    pkg.PackageInfo.SLSASourceURI(),
				SourceTag:    version,
				ArtifactPath: path,
			})
		}},
		{"minisign", toolMinisign, asset.Minisign.GetEnabled(), func() error {
			return v.minisign.Verify(ctx, logger, rt, asset.Minisign, art, file, &minisign.ParamVerify{
				ArtifactPath: path,
				PublicKey:    asset.Minisign.PublicKey,
			})
		}},
		{"github_artifact_attestations", toolGH, asset.GitHubArtifactAttestations.GetEnabled(), func() error {
			return v.attestation(ctx, logger, asset, path)
		}},
	} {
		if !check.enabled {
			continue
		}
		if err := v.ensure(ctx, logger, check.tool); err != nil {
			failed.note(check.kind, err)
			continue
		}
		if err := check.verify(); err != nil {
			failed.note(check.kind, err)
		}
	}

	if failed.err != nil {
		return fmt.Errorf("%w: %w", ErrUnverified, failed.err)
	}
	return nil
}

// unverified collects the checks that didn't hold. Each is logged as it happens,
// because a run is read as a log, and the first is what the caller is told: an asset
// that failed one check has failed, and which one is in the log above.
type unverified struct {
	logger  *slog.Logger
	pkgName string
	version string
	asset   *generate.Asset
	err     error
}

func (u *unverified) note(kind string, err error) {
	u.logger.Warn("the asset can't be verified the way its entry says it can",
		"package", u.pkgName, "version", u.version,
		"os", u.asset.OS, "arch", u.asset.Arch, "kind", kind, "error", err.Error())
	if u.err == nil {
		u.err = fmt.Errorf("%s: %w", kind, err)
	}
}

// ghExe is the GitHub CLI to read an attestation's signer with, found once.
//
// The tool has been installed by the time this is asked for, so aqua's own copy is
// what it finds; the machine's own is the fallback for an environment the package
// doesn't cover, where installing it was never going to work.
func (v *Verifier) ghExe(ctx context.Context) string {
	v.ghOnce.Do(func() {
		v.gh = ghPath(ctx)
	})
	return v.gh
}

// ensure installs the tool a check is about to run, if it isn't installed already.
// A check with no tool of its own asks for nothing.
func (v *Verifier) ensure(ctx context.Context, logger *slog.Logger, name string) error {
	t, ok := v.tools[name]
	if !ok {
		return nil
	}
	return t.ensure(ctx, logger, v.installer, v.rt)
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
