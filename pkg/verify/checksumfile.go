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

// CheckChecksumFiles holds each entry's asset to what the release's signed checksum files
// list for it.
//
// The signature is checked first, the way an asset's is, and the file is read only once it
// holds. An asset the file lists under another digest isn't what the release published,
// which is ErrTampered; an asset the file doesn't list is nothing the file says anything
// about.
//
// Which algorithm the file lists is read off each digest's length rather than the file's
// name: caddy's checksums.txt lists SHA512. A SHA256 is held to the entry's checksum, and
// any other to what the asset hashed to when it was downloaded. An asset that wasn't
// downloaded has only its SHA256, so a digest of another algorithm says nothing checkable
// about it.
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
			if !ok {
				continue
			}
			algo := algorithmOf(want)
			got := v.digestOf(version, asset, algo)
			if got == "" {
				logger.Debug("the checksum file lists a digest nothing here can be compared with",
					"checksum_file", file.Asset, "asset", asset.Asset, "algorithm", algo)
				continue
			}
			if !strings.EqualFold(want, got) {
				return fmt.Errorf("%w: %s lists %s %s for %s, which hashes to %s",
					ErrTampered, file.Asset, algo, want, asset.Asset, got)
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

// digestOf returns what the entry's asset hashes to under algo, or an empty string when
// nothing here knows: an algorithm the length named none of, or an asset that wasn't
// downloaded and so has only the SHA256 its entry records.
func (v *Verifier) digestOf(version string, asset *generate.Asset, algo string) string {
	if algo == "" {
		return ""
	}
	if algo == algoSHA256 && asset.Checksum != "" {
		return asset.Checksum
	}
	url, err := downloadURL(version, asset)
	if err != nil {
		return ""
	}
	return v.digests.of(url, algo)
}

// CheckListedChecksum compares an asset already downloaded by Verify with what a checksum
// file lists for it, for a package served as a template, whose entries carry no checksum
// of their own.
//
// The file isn't required to be signed: what this establishes is that the definition says
// where the checksums are and how they are written, which aqua relies on once the file is
// signed. An asset the file doesn't list is an error, since the definition says it does.
func (v *Verifier) CheckListedChecksum(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset, checksumURL, assetName string, cfg *aquaregistry.Checksum) error {
	dir, err := os.MkdirTemp("", "ar2-checksums")
	if err != nil {
		return fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checksums")
	logger.Debug("downloading the checksum file the definition names", "url", checksumURL)
	if _, err := v.download(ctx, checksumURL, path); err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the checksum file: %w", err)
	}
	want, err := checksum.GetChecksum(logger, assetName, string(content), cfg)
	if err != nil {
		return fmt.Errorf("find the asset in the checksum file: %w", err)
	}
	algo := algorithmOf(want)
	got := v.digestOf(version, asset, algo)
	if got == "" {
		logger.Info("the checksum file lists a digest nothing here can be compared with",
			"asset", assetName, "algorithm", algo)
		return nil
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("%w: the checksum file lists %s %s for %s, which hashes to %s",
			ErrTampered, algo, want, assetName, got)
	}
	return nil
}
