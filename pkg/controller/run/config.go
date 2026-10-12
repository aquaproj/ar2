package run

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/aquaproj/ar2/pkg/registry"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/state"
)

// newConfig is the definition a run writes for a package aqua-registry-g2 hasn't
// taken over yet: the file to commit, the definition itself, and whether it has to
// be looked at before merging.
//
// The definition is kept alongside the file because the catalogue is built from it.
// The registry doesn't hold it until this pull request merges, so the run that writes
// it is the first thing that can describe the package.
type newConfig struct {
	file        *g2.File
	config      *aquag2.Config
	needsReview bool
	// unconverted names the version_constraints that couldn't be turned into
	// boundaries. They are why the pull request needs review, so whoever reads it
	// shouldn't have to find the run's log to learn that.
	unconverted []string
}

// packageConfig returns the package definition to commit, or nil when the registry
// already has one.
//
// aqua-registry-g2 generates registry.json from a definition kept in the package's
// own directory. A package that doesn't have one yet still has its definition in
// aqua-registry, so it is converted the first time the package is worked on. The
// conversion travels with the files generated from it, which is what makes the move
// happen package by package instead of as a migration of its own.
func (c *Controller) packageConfig(logger *slog.Logger, def *definition, pkgName string, versions []*version) (*newConfig, error) {
	if def.held || def.config == nil {
		// The registry already has one, which is every package but the first time it
		// is worked on, or aqua-registry has none to convert. Nothing to write is
		// the ordinary outcome, not a failure.
		return nil, nil //nolint:nilnil
	}

	// After the versions, because what a release was signed by is read from them.
	pinSigner(logger, pkgName, def.config, versions)

	content, err := marshalConfig(def.config)
	if err != nil {
		return nil, err
	}
	return &newConfig{
		file:        &g2.File{Path: g2.ConfigFileName, Content: content},
		config:      def.config,
		needsReview: len(def.unconverted) > 0,
		unconverted: def.unconverted,
	}, nil
}

// definition is the package's aqua-registry-g2 definition, and what the conversion
// couldn't read when it had to produce one.
type definition struct {
	config *aquag2.Config
	// held says the registry already holds the definition, so there is nothing to
	// commit and nothing was converted.
	held        bool
	unconverted []string
}

// resolveDefinition returns the definition to generate from: the one on the package's
// branch, or the one converted from aqua-registry when the branch has none.
//
// The conversion happens before anything is generated rather than after. The
// definition carries the filters that decide which assets and which versions are
// considered at all -- all_assets_filter keeps a release's rocm or jetpack build from
// being taken for the ordinary one -- so generating without it produces a
// registry.json that the definition committed beside it would not produce.
func (c *Controller) resolveDefinition(ctx context.Context, logger *slog.Logger, input *Input, pkgName string) (*definition, error) {
	cfg, err := c.g2.Config(ctx, pkgName)
	if err != nil {
		return nil, fmt.Errorf("get the package definition: %w", err)
	}
	if cfg != nil && !g2.IsClaim(cfg) {
		return &definition{config: cfg, held: true}, nil
	}
	// A definition that says nothing but the package's name is the claim the branch was
	// created with, which is a package waiting to be taken over rather than one that has
	// been.

	base := input.PkgInfos[pkgName]
	if base == nil {
		// aqua-registry doesn't have the package, so there is nothing to convert.
		// What is generated comes from the release alone, and the definition is
		// written by whoever reviews it.
		logger.Debug("no aqua-registry definition to convert", "package", pkgName)
		return &definition{}, nil
	}

	scaffold, err := registry.FetchScaffold(ctx, c.gh, input.RegistryRef, pkgName)
	if err != nil {
		// The filters are what a release can't be read for, so losing them would
		// produce a definition that quietly resolves differently. Better to leave
		// the package alone and try again next run.
		return nil, fmt.Errorf("get the aqua gr configuration: %w", err)
	}

	converted, unconverted := migrate.Config(pkgName, base, scaffold)
	for _, constraint := range unconverted {
		logger.Warn("a version_constraint couldn't be turned into a boundary",
			"package", pkgName, "version_constraint", constraint)
	}
	addFormerNames(logger, converted, pkgName, input.State)
	return &definition{config: converted, unconverted: unconverted}, nil
}

// addFormerNames makes the definition say which names the package used to answer to.
//
// A package whose repository is renamed after the registry holds it keeps the old name as
// an alias, because the rename carries its branch over and writes the alias as it goes.
// A package renamed before it was ever generated has no branch to carry, so the rename
// moved its name and nothing else, and the only record of the old name is the one the
// state keeps. Without this the package arrives under its new name alone, and a
// configuration asking for the old one -- which is the name aqua-registry still has --
// resolves to nothing.
func addFormerNames(logger *slog.Logger, cfg *aquag2.Config, pkgName string, s *state.State) {
	if cfg == nil || cfg.PackageInfo == nil || s == nil {
		return
	}
	for _, former := range slices.Sorted(maps.Keys(s.Renamed)) {
		if s.Renamed[former] != pkgName || hasAlias(cfg, former) {
			continue
		}
		logger.Info("the package answers to a name it had before",
			"package", pkgName, "former_name", former)
		cfg.Aliases = append(cfg.Aliases, &aquaregistry.Alias{Name: former})
	}
}

// hasAlias says the definition already lists the name.
func hasAlias(cfg *aquag2.Config, name string) bool {
	for _, alias := range cfg.Aliases {
		if alias != nil && alias.Name == name {
			return true
		}
	}
	return false
}

// marshalConfig renders the definition the way the registry stores one. See
// g2.MarshalConfig.
func marshalConfig(cfg *aquag2.Config) (string, error) {
	return g2.MarshalConfig(cfg) //nolint:wrapcheck // the error says what it couldn't marshal
}

// pinSigner records who signs the package's releases, so that a later one signed by
// somebody else can be told apart.
//
// The generated files carry the signer read off the signature, which is a fact about
// the release that was just looked at. Writing it into the definition turns it into
// what the package is expected to be signed by: every version after this is verified
// against it, and one that doesn't match can't merge itself.
//
// A definition that already says who signs is left alone. It was written by someone
// who knows the package, and this would only replace it with whatever signed the
// release ar2 happened to look at.
func pinSigner(logger *slog.Logger, pkgName string, cfg *aquag2.Config, versions []*version) {
	if cfg == nil || cfg.PackageInfo == nil {
		return
	}
	if !cfg.Cosign.GetEnabled() {
		if signer := observedSigner(versions); signer != nil && templatable(signer) {
			cosign := templateSigner(signer)
			logger.Info("recording who signs the package", "package", pkgName,
				"identity", identityOf(cosign.Opts))
			cfg.Cosign = cosign
		}
	}
	if cfg.Packslip == nil {
		if ps := observedPackslip(versions); ps != nil {
			logger.Info("recording who signs the package's packslip bundle", "package", pkgName,
				"identity_prefix", ps.IdentityPrefix)
			cfg.Packslip = ps
		}
	}
	if cfg.GitHubArtifactAttestations.SignerWorkflow() == "" {
		if attested := observedAttestation(versions); attested != nil {
			logger.Info("recording which workflow attests the package", "package", pkgName,
				"signer_workflow", attested.SignerWorkflow())
			cfg.GitHubArtifactAttestations = attested
		}
	}
}

// observedAttestation returns the attestation configuration the generated files
// ended up with, once the workflow that signs has been read off one.
//
// Only what holds for every version goes into the definition: the workflow and what the
// attestation is about. The ref and the commit a version was built from are that version's,
// and a definition recording them would hold every later version to one build.
func observedAttestation(versions []*version) *aquaregistry.GitHubArtifactAttestations {
	for _, v := range versions {
		for _, asset := range v.Registry.Assets {
			gaa := asset.GitHubArtifactAttestations
			if gaa.SignerWorkflow() != "" {
				return &aquaregistry.GitHubArtifactAttestations{
					Enabled:         gaa.Enabled,
					PredicateType:   gaa.PredicateType,
					SignerWorkflow2: gaa.SignerWorkflow(),
				}
			}
		}
	}
	return nil
}

// observedPackslip returns the packslip configuration every version is held to, from what
// the generated files ended up with.
//
// An entry names the signer's identity with the ref of the release it was read off, which
// is that version's. The definition names the repository the signer's workflow is in, as
// aqua gr does: every release is signed from it, under its own ref. A key or a fingerprint
// is the same for every release and is recorded as it is.
func observedPackslip(versions []*version) *aquaregistry.Packslip {
	for _, v := range versions {
		for _, asset := range v.Registry.Assets {
			ps := asset.Packslip
			if !ps.GetEnabled() {
				continue
			}
			out := &aquaregistry.Packslip{PublicKey: ps.PublicKey, Pin: ps.Pin, Issuer: ps.Issuer}
			if prefix := identityRepository(ps.Identity); prefix != "" {
				out.IdentityPrefix = prefix
			} else {
				out.IdentityPrefix = ps.IdentityPrefix
			}
			if out.PublicKey == "" && out.Pin == "" && out.IdentityPrefix == "" {
				continue
			}
			return out
		}
	}
	return nil
}

// identityRepository is the repository part of a workflow identity, as the prefix every
// release's identity starts with:
//
//	https://github.com/jdx/hk/.github/workflows/release.yml@refs/tags/v2.5.0
//	https://github.com/jdx/hk/
func identityRepository(identity string) string {
	const prefix = "https://github.com/"
	rest, ok := strings.CutPrefix(identity, prefix)
	if !ok {
		return ""
	}
	parts := strings.SplitN(rest, "/", 3) //nolint:mnd
	if len(parts) < 3 {                   //nolint:mnd
		return ""
	}
	return prefix + parts[0] + "/" + parts[1] + "/"
}

// observed is what one generated entry was signed with: the entry's cosign written back
// as a command line, and what it was written from.
type observed struct {
	cosign *aquaregistry.Cosign
	// raw is the entry's cosign as it was, which still says where each file comes from.
	raw *aquaregistry.Cosign
	// asset is the entry's asset name, which a bundle on the release is named after.
	asset   string
	version string
}

// observedSigner returns the cosign configuration the generated files ended up with.
//
// The newest version is asked first, because that is the one whose signer the
// package should be held to. Every environment of a release is signed by the same
// thing, so the first entry carrying one answers for all of them.
//
// An entry holds cosign as fields, and a definition as a command line, so the entry's is
// written back as one first.
func observedSigner(versions []*version) *observed {
	for _, v := range versions {
		for _, asset := range v.Registry.Assets {
			if asset.Cosign == nil {
				continue
			}
			cosign := sign.Unstructure(asset.Cosign, asset, v.Version)
			if identityOf(cosign.Opts) != "" {
				return &observed{cosign: cosign, raw: asset.Cosign, asset: asset.Asset, version: v.Version}
			}
		}
	}
	return nil
}

// templateSigner turns what one release was signed by into what every release is
// expected to be signed by.
//
// A workflow signs under the ref it ran for, so its name holds the version of the
// release that was looked at. The generated files record that as it stands, because
// each describes one version; the definition applies to all of them and puts the
// version back as a template.
func templateSigner(o *observed) *aquaregistry.Cosign {
	out := *o.cosign
	out.Opts = make([]string, len(o.cosign.Opts))
	for i, opt := range o.cosign.Opts {
		out.Opts[i] = sign.TemplateVersion(opt, o.version)
	}
	// The bundle is named after the asset, which differs per environment, so the
	// definition refers to it the way a registry does, keeping whatever the release
	// appends to the asset's name: .sigstore.json, .bundle, .cosign.bundle.
	if out.Bundle != nil {
		bundle := *out.Bundle
		asset := "{{.Asset}}" + strings.TrimPrefix(*bundle.Asset, o.asset)
		bundle.Asset = &asset
		bundle.URL = nil
		out.Bundle = &bundle
	}
	return &out
}

// templatable says whether what one release was signed with can be written as what every
// release is signed with.
//
// What can be is a bundle on the release named after the asset, which templateSigner
// writes as {{.Asset}} and the rest of its name, and a key, which is the same file for
// every environment. A bundle fetched from a URL has the version and the architecture
// written into the URL -- cri-o/cri-o's is
// https://storage.googleapis.com/cri-o/artifacts/cri-o.amd64.v1.37.2.tar.gz.bundle -- and
// a signature or a certificate named after the asset is one environment's file. Nothing
// says which part of their names is which, so the definition is left to say it.
func templatable(o *observed) bool {
	if perEnvironment(o.raw.Signature, o.asset) || perEnvironment(o.raw.Certificate, o.asset) {
		return false
	}
	b := o.raw.Bundle
	if b == nil {
		return true
	}
	if b.Type != "" && b.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return false
	}
	return b.Asset != nil && o.asset != "" && strings.HasPrefix(*b.Asset, o.asset)
}

// perEnvironment says whether a file cosign is given differs by environment: one fetched
// from a URL, which names it as it stands, or one on the release named after the asset. A
// file every asset is checked against, such as a signature of the checksums, is the same
// for every environment.
func perEnvironment(f *aquaregistry.DownloadedFile, asset string) bool {
	if f == nil {
		return false
	}
	if f.Type != "" && f.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return true
	}
	return f.Asset != nil && asset != "" && strings.HasPrefix(*f.Asset, asset)
}

// identityOf returns the signer a cosign configuration names, or an empty string
// when it only has a pattern to match against.
func identityOf(opts []string) string {
	for i, opt := range opts {
		if opt == "--certificate-identity" && i+1 < len(opts) {
			return opts[i+1]
		}
	}
	return ""
}
