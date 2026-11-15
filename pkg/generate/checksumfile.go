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
	// Algorithm is the hash the file lists, which is always the one every entry records.
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
	return g.signedChecksumFiles(ctx, logger, input, names)
}

// signedChecksumFiles returns the checksum files of the release that cosign signs.
//
// A checksum file is how a release vouches for its assets as a whole: goreleaser publishes
// checksums.txt with a signature, and none for each asset. Each entry carries the digest of
// its own asset, so the file adds nothing to the file generated. What it adds is a check:
// an asset whose digest disagrees with what the signed file lists isn't what the release
// published, however it got there.
//
// Only the files listing sha256 are read, which is the hash every entry records. A file
// nothing signs vouches for nothing, and is left alone. The signer is read off the
// signature, the way it is for an asset.
func (g *Generator) signedChecksumFiles(ctx context.Context, logger *slog.Logger, input *Input, names map[string]struct{}) []*ChecksumFile {
	owner, name, err := repo(input.PkgName)
	if err != nil {
		return nil
	}
	var out []*ChecksumFile
	for _, file := range slices.Sorted(maps.Keys(names)) {
		cfg := checksum.GetChecksumConfigFromFilename(file, input.Version)
		if cfg == nil || cfg.Algorithm != checksumAlgorithm {
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
