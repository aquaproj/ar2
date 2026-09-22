package run

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/migrate"
	"github.com/szksh-lab-2/ar2/pkg/registry"
	"go.yaml.in/yaml/v3"
)

// newConfig is the definition a run writes for a package aqua-registry-g2 hasn't
// taken over yet: the file to commit, the definition itself, and whether it has to
// be looked at before merging.
//
// The definition is kept alongside the file because the catalogue is built from it.
// The branch doesn't hold it until this pull request merges, so the run that writes
// it is the first thing that can describe the package.
type newConfig struct {
	file        *g2.File
	config      *aquag2.Config
	needsReview bool
}

// packageConfig returns the package definition to commit, or nil when the branch
// already has one.
//
// aqua-registry-g2 generates registry.json from a definition kept on the package's
// own branch. A package that doesn't have one yet still has its definition in
// aqua-registry, so it is converted the first time the package is worked on. The
// conversion travels with the files generated from it, which is what makes the move
// happen package by package instead of as a migration of its own.
func (c *Controller) packageConfig(ctx context.Context, logger *slog.Logger, input *Input, config *aquag2.Config, pkgName string, versions []*version) (*newConfig, error) {
	if config != nil {
		// The branch already has one, which is every package but the first time it
		// is worked on. Nothing to write is the ordinary outcome, not a failure.
		return nil, nil //nolint:nilnil
	}

	base := input.PkgInfos[pkgName]
	if base == nil {
		// aqua-registry doesn't have the package, so there is nothing to convert.
		// What was generated came from the release alone, and the definition is
		// written by whoever reviews it.
		logger.Debug("no aqua-registry definition to convert", "package", pkgName)
		return nil, nil //nolint:nilnil
	}

	scaffold, err := registry.FetchScaffold(ctx, c.gh, input.RegistryRef, pkgName)
	if err != nil {
		// The filters are what a release can't be read for, so losing them would
		// produce a definition that quietly resolves differently. Better to leave
		// the package without one and try again next run.
		return nil, fmt.Errorf("get the aqua gr configuration: %w", err)
	}

	cfg, unconverted := migrate.Config(base, scaffold)
	pinSigner(logger, pkgName, cfg, versions)
	for _, constraint := range unconverted {
		logger.Warn("a version_constraint couldn't be turned into a boundary",
			"package", pkgName, "version_constraint", constraint)
	}

	content, err := marshalConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &newConfig{
		file:        &g2.File{Path: g2.ConfigFileName, Content: content},
		config:      cfg,
		needsReview: len(unconverted) > 0,
	}, nil
}

// yamlIndent is how far a registry file indents, which is what aqua-registry uses.
const yamlIndent = 2

// marshalConfig renders the definition the way aqua-registry writes one.
//
// The indentation is set rather than left to the encoder, whose default is four
// spaces. Registry files are written and read by people, and one that doesn't look
// like the others is one more thing to notice.
func marshalConfig(cfg *aquag2.Config) (string, error) {
	buf := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buf)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(cfg); err != nil {
		return "", fmt.Errorf("marshal the package definition: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("close the YAML encoder: %w", err)
	}
	return buf.String(), nil
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
	if cfg == nil || cfg.PackageInfo == nil || cfg.Cosign.GetEnabled() {
		return
	}
	signer := observedSigner(versions)
	if signer == nil {
		return
	}
	logger.Info("recording who signs the package", "package", pkgName,
		"identity", identityOf(signer.Opts))
	cfg.Cosign = signer
}

// observedSigner returns the cosign configuration the generated files ended up with.
//
// The newest version is asked first, because that is the one whose signer the
// package should be held to. Every environment of a release is signed by the same
// thing, so the first entry carrying one answers for all of them.
func observedSigner(versions []*version) *aquaregistry.Cosign {
	for _, v := range versions {
		for _, asset := range v.Registry.Assets {
			if asset.Cosign == nil {
				continue
			}
			if identityOf(asset.Cosign.Opts) != "" {
				return asset.Cosign
			}
		}
	}
	return nil
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
