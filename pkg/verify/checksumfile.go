package verify

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/checksum"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

// CheckChecksumFiles holds each entry's checksum to what the release's signed checksum files
// list for its asset.
//
// The signature is checked first, the way an asset's is, and the file is read only once it
// holds. An asset the file lists under another digest isn't what the release published,
// which is ErrTampered; an asset the file doesn't list is nothing the file says anything
// about.
func (v *Verifier) CheckChecksumFiles(ctx context.Context, logger *slog.Logger, pkgName, version string, reg *generate.Registry, files []*generate.ChecksumFile) error {
	if v.signatures == nil || len(files) == 0 || len(reg.Assets) == 0 {
		return nil
	}
	for _, file := range files {
		listed, err := v.checksumFile(ctx, logger, pkgName, version, reg.Assets[0], file)
		if err != nil {
			return fmt.Errorf("check the checksum file %s: %w", file.Asset, err)
		}
		for _, asset := range reg.Assets {
			want, ok := checksum.FindChecksum(listed, asset.Asset)
			if !ok || asset.Checksum == "" {
				continue
			}
			if !strings.EqualFold(want, asset.Checksum) {
				return fmt.Errorf("%w: %s lists %s for %s, which hashes to %s",
					ErrTampered, file.Asset, want, asset.Asset, asset.Checksum)
			}
		}
		logger.Debug("the entries agree with the signed checksum file", "checksum_file", file.Asset)
	}
	return nil
}

// checksumFile downloads the checksum file, checks its signature, and returns what it lists,
// by asset name.
//
// It is downloaded from the release the entry's asset is in, which is the release the file
// was read off.
func (v *Verifier) checksumFile(ctx context.Context, logger *slog.Logger, pkgName, version string, of *generate.Asset, file *generate.ChecksumFile) (map[string]string, error) {
	asset := &generate.Asset{
		OS:        of.OS,
		Arch:      of.Arch,
		Type:      of.Type,
		RepoOwner: of.RepoOwner,
		RepoName:  of.RepoName,
		Host:      of.Host,
		Asset:     file.Asset,
		Cosign:    file.Cosign,
	}
	url, err := downloadURL(version, asset)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "ar2-checksums")
	if err != nil {
		return nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, assetFileName(url, asset))
	logger.Debug("downloading the checksum file to check the entries against it", "url", url)
	if _, err := v.download(ctx, url, path); err != nil {
		return nil, err
	}
	if err := v.signatures.Check(ctx, logger, pkgName, version, asset, path); err != nil {
		return nil, err //nolint:wrapcheck // it already names the signature and why
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the checksum file: %w", err)
	}
	listed, _, err := checksum.ParseChecksumFile(string(content), &aquaregistry.Checksum{
		Type:      aquaregistry.PkgInfoTypeGitHubRelease,
		Asset:     file.Asset,
		Algorithm: file.Algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("read what the checksum file lists: %w", err)
	}
	return listed, nil
}
