package generate

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
)

// Input holds the parameters of a single generation.
type Input struct {
	// PkgName is the aqua package name, e.g. "cli/cli".
	PkgName string
	// Version is the release tag, e.g. "v2.101.0".
	Version string
	// Scaffold is the aqua gr configuration of the package, taken from
	// aqua-registry's scaffold.yaml. It supplies the filters that can't be inferred
	// from the release, such as which assets to ignore. It may be nil.
	Scaffold *genrgst.RawConfig
	// Base is the package's definition in aqua-registry, already resolved for the
	// version. It supplies what a release can't express: files, libc variants, and
	// the signing configuration. It may be nil for a package aqua-registry doesn't
	// have yet.
	Base *aquaregistry.PackageInfo
	// Config is the package's definition on its aqua-registry-g2 branch. When it is
	// set it replaces Base and Scaffold, because it is what those were converted
	// into: aqua-registry is where a package's definition comes from until
	// aqua-registry-g2 has one of its own, and after that it is no longer consulted.
	Config *g2.Config
	// Repos is where the release is read from, when that is not GitHub. A package on
	// a forge instance is read from the instance, by a client that answers what
	// GitHub's does: the asset naming is then inferred by the same code, and only the
	// host it was read from differs. It may be nil, which is GitHub.
	Repos genrgst.RepositoriesService
}

// Generator builds registry.json from a release.
type Generator struct {
	gh genrgst.RepositoriesService
	// httpClient downloads the signature bundle beside an asset, which is the only
	// thing here read from a release rather than from its asset list.
	httpClient *http.Client
}

// New creates a Generator.
func New(gh genrgst.RepositoriesService, httpClient *http.Client) *Generator {
	return &Generator{gh: gh, httpClient: httpClient}
}

// Generate resolves a release into registry.json.
//
// The asset of each environment is picked from the release's own asset list by name,
// rather than rendered from a template in a registry, which is what stops an upstream
// renaming from breaking the registry. The definition supplies what a release can't say,
// such as the executables inside the archive and how the assets are signed.
//
// What it returns besides the file are the environments the file leaves out, and why, and
// the signed checksum files of the release, which say what each asset should hash to.
func (g *Generator) Generate(ctx context.Context, logger *slog.Logger, input *Input) (*Generated, error) {
	input, err := useConfig(logger, input)
	if err != nil {
		return nil, err
	}
	base, err := resolveBase(logger, input)
	if err != nil {
		return nil, err
	}

	// Only a release exposes an asset list to pick from. For every other type the
	// URL or the path is a template that nothing but registry.yaml knows, so its
	// definition is used as it is.
	if base != nil && !inferable(base) {
		reg, err := resolve(logger, input.PkgName, base, input.Version, nil)
		if err != nil {
			return nil, err
		}
		// No release is read for these, so a version is a prerelease by how its tag is
		// written.
		reg.Prerelease = github.IsPrerelease(input.Version)
		return &Generated{Registry: reg}, nil
	}

	rel, err := g.release(ctx, input, base)
	if err != nil {
		return nil, err
	}
	reg, left, err := selectAssets(logger, input, base, rel)
	if err != nil {
		return nil, err
	}
	// What the release says about itself, which the version string doesn't: a reader
	// asking whether a version is old enough to install has nothing else to ask.
	reg.PublishedAt = rel.publishedAt
	reg.Prerelease = rel.prerelease
	overrideSigning(reg, base)
	// Before the inference, so that a release which moved from one way of signing to
	// another has the old one dropped and the new one read off the same asset list.
	dropUnpublished(logger, reg, rel.names)
	declared := signingKinds(reg)
	g.inferProvenance(ctx, logger, input, base, reg, rel.names)
	out := &Generated{Registry: reg, Left: left}
	defer func() { out.Inferred = inferredKinds(declared, reg) }()
	// What is inferred about signing is GitHub's: the identity it assumes is a
	// workflow of the package's repository on github.com, which a release on an
	// instance is not signed by. A definition naming cosign itself is left alone,
	// which is the only way such a release says who signed it.
	if !OnInstance(base) {
		if err := g.inferSigning(ctx, logger, input, reg, rel.names); err != nil {
			return nil, err
		}
		out.ChecksumFiles = g.signedChecksumFiles(ctx, logger, input, rel.names)
	}
	return out, nil
}

// inferProvenance records how the provenance the release publishes is verified, reading
// what each one is. A release on an instance isn't read: a provenance there names no
// builder GitHub's verifiers trust.
func (g *Generator) inferProvenance(ctx context.Context, logger *slog.Logger, input *Input, base *aquaregistry.PackageInfo, reg *Registry, names map[string]struct{}) {
	owner, name, err := repoOf(input, base)
	if OnInstance(base) || err != nil {
		inferSLSA(reg, base, names, slsaOnly)
		return
	}
	inferSLSA(reg, base, names, g.newProvenances(ctx, logger, owner, name, input.Version).of)
}

// slsaOnly takes every provenance for SLSA's, which is what was assumed of them before they
// were read.
func slsaOnly(string) provenanceInfo {
	return provenanceInfo{kind: provenanceSLSA}
}

// Generated is what generating a version comes to.
type Generated struct {
	// Registry is the file.
	Registry *Registry
	// Left are the environments the file leaves out, and why. They say nothing about the
	// environments that are in the file, which keep the version.
	Left []*Left
	// ChecksumFiles are the release's checksum files that a signature vouches for.
	ChecksumFiles []*ChecksumFile
	// Inferred are the kinds of signing the entries carry because they were read off the
	// release rather than written in the definition: "slsa_provenance", "cosign",
	// "packslip".
	Inferred map[string]bool
}

// useConfig replaces the aqua-registry definition with aqua-registry-g2's own when
// the package has one.
//
// The two say the same things; g2's is what aqua-registry's was converted into, minus
// what a release is read for. Preferring it is what makes the conversion take effect:
// the filters it carries are what decide which assets are considered at all, and a
// correction made to it would otherwise never be used.
//
// Base is already resolved for the version by resolveBase, so the definition is
// handed over in the same shape.
func useConfig(logger *slog.Logger, input *Input) (*Input, error) {
	if input.Config == nil {
		return input, nil
	}
	pkgInfo, err := input.Config.SetVersion(logger, input.Version)
	if err != nil {
		return nil, fmt.Errorf("resolve the package definition for the version: %w", err)
	}
	// SetVersion has applied the overrides, so resolveBase must not apply them
	// again.
	pkgInfo.VersionConstraints = ""
	pkgInfo.VersionOverrides = nil

	replaced := *input
	replaced.Base = pkgInfo
	replaced.Scaffold = &genrgst.RawConfig{
		AllAssetsFilter: input.Config.AllAssetsFilter,
		// Which asset is the command can change over a package's history, so the
		// generator is told the whole axis and picks the entry for this version.
		VersionOverrides: rawAssetFilters(input.Config.AssetFilters),
		VersionFilter:    pkgInfo.VersionFilter,
		VersionPrefix:    pkgInfo.VersionPrefix,
	}
	return &replaced, nil
}

// rawAssetFilters hands the definition's version axis to the generator, which reads it
// the way a registry reads its version_overrides.
func rawAssetFilters(filters []*g2.AssetFilter) []*genrgst.RawVersionOverride {
	if len(filters) == 0 {
		return nil
	}
	out := make([]*genrgst.RawVersionOverride, 0, len(filters))
	for _, f := range filters {
		if f == nil {
			continue
		}
		out = append(out, &genrgst.RawVersionOverride{
			VersionConstraint: f.VersionConstraint,
			AllAssetsFilter:   f.AllAssetsFilter,
		})
	}
	return out
}

// resolveBase applies the version_overrides of aqua-registry's definition, so that
// what is merged is the definition for this version rather than the latest one.
func resolveBase(logger *slog.Logger, input *Input) (*aquaregistry.PackageInfo, error) {
	if input.Base == nil {
		return nil, nil //nolint:nilnil
	}
	base, err := input.Base.SetVersion(logger, input.Version)
	if err != nil {
		return nil, fmt.Errorf("apply version_overrides of the aqua-registry definition: %w", err)
	}
	return base, nil
}

// release is what a release says about its own assets.
type release struct {
	// digests holds the SHA256 digest of each asset that has one, keyed by name.
	//
	// GitHub started exposing digests on 2025-06-03 and doesn't backfill them, so
	// this is empty for older releases. Those are downloaded and hashed instead.
	digests map[string]string
	// names holds every asset name, which is what the signatures are found by.
	names map[string]struct{}
	// publishedAt is when the release was published, as RFC 3339. A draft that was
	// never published has no such moment, and then it is empty.
	publishedAt string
	// published is the same moment, as a time.
	published time.Time
	// prerelease says the release is marked as a prerelease.
	prerelease bool
}

// release reads the release's asset list.
//
// The list comes with the release rather than being fetched separately, which is one
// request instead of two per version. Assets whose upload never completed are left
// out: GitHub keeps them in the "starter" state, where they are hidden from the
// release page and can't be downloaded. A release left with none is an
// EmptyReleaseError.
func (g *Generator) release(ctx context.Context, input *Input, base *aquaregistry.PackageInfo) (*release, error) {
	owner, name, err := repoOf(input, base)
	if err != nil {
		return nil, err
	}
	rel, _, err := g.repos(input).GetReleaseByTag(ctx, owner, name, input.Version)
	if err != nil {
		return nil, fmt.Errorf("get the release: %w", err)
	}
	out := &release{
		digests:     map[string]string{},
		names:       map[string]struct{}{},
		publishedAt: publishedAt(rel.GetPublishedAt().Time),
		published:   rel.GetPublishedAt().Time,
		prerelease:  rel.GetPrerelease(),
	}
	for _, asset := range rel.Assets {
		if asset.GetState() != assetStateUploaded {
			continue
		}
		out.names[asset.GetName()] = struct{}{}
		if digest := asset.GetDigest(); digest != "" {
			out.digests[asset.GetName()] = strings.TrimPrefix(digest, "sha256:")
		}
	}
	if len(out.names) == 0 {
		return nil, &EmptyReleaseError{PublishedAt: out.published}
	}
	return out, nil
}

// assetStateUploaded is the state of an asset whose upload has completed.
const assetStateUploaded = "uploaded"

// publishedAt is when a release was published, written the one way every reader of the
// generated file can compare: RFC 3339, in UTC. The zero time is a release that has no
// such moment rather than one published at the beginning of the epoch.
func publishedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// repo returns the repository a package is released from.
//
// A package name can have more than two segments — a monorepo publishing several
// binaries — and the repository is the first two.
// inferable says whether the release publishes an asset list for the naming to be read
// from.
//
// A GitHub release does, and so does a release on a forge instance: a client for the
// instance answers the same things in the same words, which is why one inference reads
// them all.
func inferable(base *aquaregistry.PackageInfo) bool {
	return base.Type == aquaregistry.PkgInfoTypeGitHubRelease ||
		aquaregistry.OnInstanceType(base.Type)
}

// OnInstance says whether the package is on a forge instance of its own rather than on
// github.com, which is what decides where its releases are read from.
//
// The instance is the definition's, and a type may name it by default: a gitlab_release
// package says no host when it is on gitlab.com, which GetHost answers with.
func OnInstance(base *aquaregistry.PackageInfo) bool {
	return base != nil && aquaregistry.OnInstanceType(base.Type) && base.GetHost() != ""
}

// repos is what the release is read from: the instance's client when the package is on
// one, and GitHub otherwise.
func (g *Generator) repos(input *Input) genrgst.RepositoriesService {
	if input.Repos != nil {
		return input.Repos
	}
	return g.gh
}

// repoOf is the repository the release is in.
//
// The package name is it for a package on github.com, and is not for one on an instance:
// codeberg.org/mergiraf/mergiraf names the instance first, and what the API is asked for
// is the owner and the name the definition gives.
func repoOf(input *Input, base *aquaregistry.PackageInfo) (string, string, error) {
	if OnInstance(base) && base.HasRepo() {
		return base.RepoOwner, base.RepoName, nil
	}
	return repo(input.PkgName)
}

func repo(pkgName string) (string, string, error) {
	owner, name, found := strings.Cut(pkgName, "/")
	if !found {
		return "", "", errPkgNameFormat
	}
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	return owner, name, nil
}
