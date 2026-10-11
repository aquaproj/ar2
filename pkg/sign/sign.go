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
	"path/filepath"
	"strings"
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
	"github.com/aquaproj/aqua/v2/pkg/packslip"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/aquaproj/aqua/v2/pkg/slsa"
	aquatemplate "github.com/aquaproj/aqua/v2/pkg/template"
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
	packslip      *packslip.Verifier
	ghattestation *ghattestation.Verifier
	// dropUnattested says an attestation GitHub doesn't hold is taken off the entry
	// rather than failing it. See DropUnattested.
	dropUnattested bool

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
	packslipExe, err := packslip.NewExecutor(logger, exe, param)
	if err != nil {
		return nil, fmt.Errorf("prepare packslip: %w", err)
	}
	attestExe, err := ghattestation.NewExecutor(exe, param)
	if err != nil {
		return nil, fmt.Errorf("prepare gh: %w", err)
	}
	cosignVerifier := cosign.NewVerifier(exe, dl, param)
	slsaVerifier := slsa.New(dl, slsa.NewExecutor(exe, param))
	minisignVerifier := minisign.New(dl, minisignExe)
	packslipVerifier := packslip.New(dl, packslipExe)
	attestVerifier := ghattestation.New(attestExe)

	rt := runtime.NewR(ctx)
	httpDL := download.NewHTTPDownloader(logger, httpClient)
	return &Verifier{
		httpClient:    httpClient,
		cosign:        cosignVerifier,
		slsa:          slsaVerifier,
		minisign:      minisignVerifier,
		packslip:      packslipVerifier,
		ghattestation: attestVerifier,
		rt:            rt,
		installer: installpackage.New(param, dl, rt, link.New(),
			download.NewChecksumDownloader(gh, rt, httpDL), checksum.NewCalculator(),
			unarchive.New(exe),
			cosignVerifier, slsaVerifier, minisignVerifier, packslipVerifier, attestVerifier,
			installpackage.NewGoInstallInstallerImpl(exe),
			installpackage.NewGoBuildInstallerImpl(exe),
			installpackage.NewCargoPackageInstallerImpl(exe),
			vacuum.New(param)),
		tools: map[string]*tool{
			toolCosign:   {pkg: cosign.Package, checksums: cosign.Checksums()},
			toolSLSA:     {pkg: slsa.Package, checksums: slsa.Checksums()},
			toolMinisign: {pkg: minisign.Package, checksums: minisign.Checksums()},
			toolPackslip: {pkg: packslip.Package, checksums: packslip.Checksums()},
			toolGH:       {pkg: ghattestation.Package, checksums: ghattestation.Checksums()},
		},
	}, nil
}

// DropUnattested makes an attestation GitHub doesn't hold come off the entry, rather than
// fail it. It is for generating.
//
// A definition says a package is attested for every version unless an override says
// otherwise, and the versions before a release started being attested don't override it.
// Failing the entry there stopped everything checked after the signatures -- the archive's
// files, the libc its executables need -- and the attestation came off later anyway, when
// GitHub was asked whether it holds one: FiloSottile/age v1.2.1 lost linked_libc that way.
//
// The registry's own check of a generated file doesn't do this. A file saying it is attested
// when it isn't is what that check is for.
func (v *Verifier) DropUnattested() {
	v.dropUnattested = true
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
	// The file the signatures are about. Its type is the package's: packslip's bundle is
	// downloaded from the same kind of release as the asset when no URL says otherwise.
	file := &download.File{
		Type:      asset.Type,
		RepoOwner: asset.RepoOwner,
		RepoName:  asset.RepoName,
		Host:      asset.Host,
		Version:   version,
	}

	failed := &unverified{logger: logger, pkgName: pkgName, version: version, asset: asset}

	// Who signed is read off the signature before it is checked, so that a guessed
	// pattern is replaced by a name and the entry records what actually signed.
	v.pin(ctx, logger, version, asset)

	for _, check := range v.checks(ctx, logger, rt, pkg, art, file, version, asset, path) {
		v.make(ctx, logger, check, failed)
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
		Packslip:                   asset.Packslip,
		SLSAProvenance:             asset.SLSAProvenance,
	}
	return &config.Package{
		Package:     &aqua.Package{Name: pkgName, Version: version},
		PackageInfo: entry.PackageInfo(),
	}
}

// attestation checks the artifact's attestation against what the entry says, and records
// on the entry what the attestation's certificate says about the run that built it: the
// workflow that signed when the definition doesn't name it, the ref and the commit it
// built from, the issuer, and whether the runner was one GitHub hosts.
//
// Both are one run of the GitHub CLI, the one that verifies: what it prints is the
// certificate it has just verified, so what is recorded is checked rather than read off a
// file nobody has checked. An entry that already says these is held to them, which is how
// the registry's own check of a generated file verifies it.
func (v *Verifier) attestation(ctx context.Context, logger *slog.Logger, asset *generate.Asset, path string) error {
	repo := asset.RepoOwner + "/" + asset.RepoName
	gaa := asset.GitHubArtifactAttestations
	cert, err := v.verifyAttestation(ctx, repo, path, gaa)
	if err != nil {
		if v.dropUnattested && notAttested(err) {
			logger.Warn("the release isn't attested, so the entry doesn't say it is",
				"os", asset.OS, "arch", asset.Arch)
			asset.GitHubArtifactAttestations = nil
			return nil
		}
		return err
	}
	if gaa.SignerWorkflow() == "" {
		signer := signerWorkflow(cert.signer())
		if signer == "" {
			return errNoIdentity
		}
		logger.Debug("read which workflow signed the attestation",
			"os", asset.OS, "arch", asset.Arch, "signer_workflow", signer)
		gaa.SignerWorkflow2 = signer
	}
	cert.pin(gaa, repo)
	return nil
}

// minisignUnrunnable says minisign can't run on this machine, and nothing when it can.
//
// The tool runs on the host rather than on the environment the entry is for, and it isn't
// built for every host aqua supports: on linux/arm64 there is no minisign to install.
// ziglang/zig publishes minisign signatures, so every entry it has said so, and the check
// that reads the entry on a Linux arm64 machine called that a registry not holding up to
// its release. aqua says the same thing about the same package and installs it anyway.
func (v *Verifier) minisignUnrunnable() string {
	supported, err := minisign.Package().PackageInfo.CheckSupported(v.rt, v.rt.Env())
	if err != nil || supported {
		// An environment that can't be decided is one the check is attempted on. What
		// comes back then is whatever the tool says, which is a better answer than a
		// guess made here.
		return ""
	}
	return "minisign isn't built for " + v.rt.Env()
}

// packslipUnrunnable says packslip can't run on this machine, and nothing when it can.
//
// Like minisign, it runs on the host and isn't built for every host: packslip ships for
// linux, darwin/arm64 and windows, and aqua skips the check elsewhere with a warning.
func (v *Verifier) packslipUnrunnable() string {
	supported, err := packslip.Package().PackageInfo.CheckSupported(v.rt, v.rt.Env())
	if err != nil || supported {
		return ""
	}
	return "packslip isn't built for " + v.rt.Env()
}

// check is one way an asset can be verified, and what it takes to make it.
type check struct {
	kind string
	// tool is what the check is run with, and what has to be installed before it can
	// be. The attestations are checked by a gh the machine already has.
	tool    string
	enabled bool
	// unrunnable says this machine can't make the check, and why. Nil is a check that
	// runs anywhere.
	unrunnable func() string
	verify     func() error
	// asset is what the check is about, for what is said when it doesn't hold.
	asset *generate.Asset
}

// checks is every way this asset says it can be verified.
func (v *Verifier) checks(ctx context.Context, logger *slog.Logger, rt *runtime.Runtime, pkg *config.Package, art *aquatemplate.Artifact, file *download.File, version string, asset *generate.Asset, path string) []*check {
	return []*check{
		{kind: "cosign", tool: toolCosign, enabled: asset.Cosign.GetEnabled(), asset: asset, verify: func() error {
			return v.cosign.Verify(ctx, logger, rt, file, asset.Cosign, art, path)
		}},
		{kind: "slsa_provenance", tool: toolSLSA, enabled: asset.SLSAProvenance.GetEnabled(), asset: asset, verify: func() error {
			return v.slsa.Verify(ctx, logger, rt, asset.SLSAProvenance, art, file, &slsa.ParamVerify{
				SourceURI:    pkg.PackageInfo.SLSASourceURI(),
				SourceTag:    version,
				ArtifactPath: path,
			})
		}},
		{kind: "minisign", tool: toolMinisign, enabled: asset.Minisign.GetEnabled(), asset: asset, unrunnable: v.minisignUnrunnable, verify: func() error {
			return v.minisign.Verify(ctx, logger, rt, asset.Minisign, art, file, &minisign.ParamVerify{
				ArtifactPath: path,
				PublicKey:    asset.Minisign.PublicKey,
			})
		}},
		{kind: "packslip", tool: toolPackslip, enabled: asset.Packslip.GetEnabled(), asset: asset, unrunnable: v.packslipUnrunnable, verify: func() error {
			// packslip looks the artifact up in its signed document by the name it is
			// published under, which is the name it was downloaded as.
			return v.packslip.Verify(ctx, logger, rt, asset.Packslip, art, file, path, filepath.Base(path))
		}},
		{kind: "github_artifact_attestations", tool: toolGH, enabled: asset.GitHubArtifactAttestations.GetEnabled(), asset: asset, verify: func() error {
			return v.attestation(ctx, logger, asset, path)
		}},
	}
}

// make runs one check, unless there is nothing to run or no way to run it here.
func (v *Verifier) make(ctx context.Context, logger *slog.Logger, c *check, failed *unverified) {
	if !c.enabled {
		return
	}
	if c.unrunnable != nil {
		if reason := c.unrunnable(); reason != "" {
			// Not a check that failed. aqua skips the same one at install time with the
			// same warning, so the entry is installable where this is true; what it says
			// about the release is checked wherever the tool does run, which is the rest
			// of the matrix.
			logger.Warn("this machine can't make the check, so it goes unmade here",
				"os", c.asset.OS, "arch", c.asset.Arch, "kind", c.kind, "reason", reason)
			return
		}
	}
	if err := v.ensure(ctx, logger, c.tool); err != nil {
		failed.note(c.kind, err)
		return
	}
	if err := c.verify(); err != nil {
		failed.note(c.kind, err)
	}
}

// notAttested reports whether gh failed because GitHub holds no attestation for the
// artifact, rather than because one didn't verify.
func notAttested(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no attestations found") ||
		(strings.Contains(msg, "HTTP 404") && strings.Contains(msg, "/attestations/"))
}
