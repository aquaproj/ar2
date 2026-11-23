package generate

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/aquaproj/aqua/v2/pkg/checksum"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
)

// ChecksumFile is a checksum file of the release and the signature that vouches for it.
type ChecksumFile struct {
	// Asset is the file's name in the release.
	Asset string
	// Algorithm is the hash the file's name says it lists. What is compared goes by each
	// digest's length instead, since a name can be wrong: caddy's checksums.txt lists SHA512.
	Algorithm string
	// Cosign is how the file is signed, held to the signer the signature names.
	Cosign *aquaregistry.Cosign
}

// checksumFiles returns the signed checksum files of the release to hold the entries to, or
// none when the definition turns their signature off: see checksumSigningOff.
func (g *Generator) checksumFiles(ctx context.Context, logger *slog.Logger, input *Input, base *aquaregistry.PackageInfo, names map[string]struct{}) []*ChecksumFile {
	if checksumSigningOff(base) {
		logger.Warn("the definition turns off the signature of the release's checksum files, so they aren't checked")
		return nil
	}
	return g.signedChecksumFiles(ctx, logger, input, names, uninferableOpts(base))
}

// signedChecksumFiles returns the checksum files of the release that cosign signs.
//
// A checksum file is how a release vouches for its assets as a whole: goreleaser publishes
// checksums.txt with a signature, and none for each asset. Each entry carries the digest of
// its own asset, so the file adds nothing to the file generated. What it adds is a check:
// an asset whose digest disagrees with what the signed file lists isn't what the release
// published, however it got there.
//
// Whatever hash a file lists, it is read: what an asset hashes to is computed under each one
// a checksum file may list as it is downloaded. A file nothing signs vouches for nothing, and
// is left alone. The signer is read off the
// signature, the way it is for an asset.
//
// extra are the options the definition gives that the signature can't tell: see
// uninferableOpts. They go after the ones read off the signature.
func (g *Generator) signedChecksumFiles(ctx context.Context, logger *slog.Logger, input *Input, names map[string]struct{}, extra []string) []*ChecksumFile {
	owner, name, err := repo(input.PkgName)
	if err != nil {
		return nil
	}
	var out []*ChecksumFile
	for _, file := range slices.Sorted(maps.Keys(names)) {
		cfg := checksum.GetChecksumConfigFromFilename(file, input.Version)
		if cfg == nil {
			continue
		}
		cosign := genrgst.InferCosign(owner, name, file, names)
		if cosign == nil {
			continue
		}
		cosign = g.heldToTheSigner(ctx, logger, owner, name, input.Version, cosign)
		if cosign == nil {
			continue
		}
		cosign.Opts = withOpts(cosign.Opts, extra)
		out = append(out, &ChecksumFile{Asset: file, Algorithm: cfg.Algorithm, Cosign: cosign})
	}
	return out
}

// checksumSigningOff is checksum.cosign.enabled: false, which the definition says for a version
// whose checksum files are signed in a way that can't be checked any more.
//
// charmbracelet/skate's v0.1.4 to v0.2.0 sign checksums.txt with a certificate from the
// Sigstore of 2022, which the trusted root cosign verifies against today no longer vouches
// for: "leaf certificate verification failed". Nothing about the release is wrong, and
// nothing a definition can say makes the signature check out, so the versions were never
// published. A person who has looked says so, for those versions, and each entry is still
// held to the digest GitHub reports for its asset.
func checksumSigningOff(base *aquaregistry.PackageInfo) bool {
	if base == nil || base.Checksum == nil || base.Checksum.Cosign == nil {
		return false
	}
	return base.Checksum.Cosign.Enabled != nil && !*base.Checksum.Cosign.Enabled
}

// uninferableFlags are the cosign options a definition may give for a checksum file's
// signature: the ones that say how far to trust it, which nothing in the release says.
//
// Who signed, and where the signature and certificate are, are read off the release, so a
// definition naming them would only be a second answer that can be wrong. Whether to accept
// a certificate without an embedded SCT, or a signature missing from the transparency log, is
// a person's decision about one release: orlangure/gocovsh's v0.5.0 and v0.5.1 sign
// checksums.txt with a certificate that carries no SCT, which cosign refuses today, and the
// signer is still the release workflow.
var uninferableFlags = map[string]struct{}{ //nolint:gochecknoglobals
	"--insecure-ignore-sct":  {},
	"--insecure-ignore-tlog": {},
}

// uninferableOpts returns the options of the definition's checksum.cosign.opts that are among
// uninferableFlags, in the order given. Any other option there is left out.
func uninferableOpts(base *aquaregistry.PackageInfo) []string {
	if base == nil || base.Checksum == nil || base.Checksum.Cosign == nil {
		return nil
	}
	var opts []string
	for _, opt := range base.Checksum.Cosign.Opts {
		if _, ok := uninferableFlags[opt]; ok {
			opts = append(opts, opt)
		}
	}
	return opts
}

// withOpts appends the options that aren't already there.
func withOpts(opts, extra []string) []string {
	for _, opt := range extra {
		if !slices.Contains(opts, opt) {
			opts = append(opts, opt)
		}
	}
	return opts
}
