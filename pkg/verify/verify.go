// Package verify extracts a generated asset to decide whether its registry.json can
// be merged without a human looking at it.
//
// Everything else in registry.json is resolved from metadata the release exposes, so
// it either matches reality or the generation fails. files[].src is different: it
// names a path inside the archive, and nothing but the archive can say whether that
// path is still right. When it isn't, carrying the template over would produce a
// registry.json that resolves cleanly and fails at install time.
//
// So the archive decides the merge policy. A package whose files all resolve is
// carried over unchanged and can be merged automatically. One whose files moved is
// relocated by name and must be reviewed, because relocating is a guess.
package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/osexec"
	"github.com/aquaproj/aqua/v2/pkg/unarchive"
	"github.com/aquaproj/ar2/pkg/generate"
)

// Verifier downloads and extracts assets.
type Verifier struct {
	httpClient *http.Client
	unarchiver *unarchive.Unarchiver
	// signatures checks that the asset carries what its entry says it does. It is
	// optional: a run that only wants the files checked doesn't install cosign and
	// the rest to do it.
	signatures Signatures
}

// Signatures verifies an asset against the signatures its entry claims, drops the
// ones that don't hold, and returns what it dropped.
type Signatures interface {
	Check(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, path string) []string
}

// New creates a Verifier.
//
// The unarchiver gets an executor, so dmg and pkg assets can be extracted where the
// tools that open them exist. Whether they do is asked before extracting rather than
// assumed from the format: the same code runs on a macOS laptop and a Linux runner,
// and only one of them can open a dmg.
func New(httpClient *http.Client, signatures Signatures) *Verifier {
	return &Verifier{
		httpClient: httpClient,
		unarchiver: unarchive.New(osexec.New()),
		signatures: signatures,
	}
}

// formatTools names the command a format is opened with, for the formats that need
// one. Everything else is unpacked in process.
var formatTools = map[string]string{ //nolint:gochecknoglobals
	unarchive.FormatDMG: "hdiutil",
	unarchive.FormatPKG: "pkgutil",
}

// Extractable reports whether this machine can open the asset.
//
// A dmg on Linux isn't a broken package, it is a package this machine can't look
// inside. Saying so lets the caller carry on with what it can establish instead of
// failing the version over where it happens to be running.
func Extractable(format string) bool {
	tool, ok := formatTools[format]
	if !ok {
		return true
	}
	_, err := exec.LookPath(tool)
	return err == nil
}

// Result is what extracting one asset established.
type Result struct {
	// Checksum is the SHA256 of the downloaded bytes. It is filled in whether or not
	// the release reported a digest, so a release published before GitHub started
	// reporting them still gets a checksum.
	Checksum string
	// Files is the files list to record. It is the input list when every src was
	// found, and a relocated one otherwise.
	Files []*generate.File
	// NeedsReview is set when a src had to be relocated or couldn't be found at all.
	// A pull request carrying such a result must not be merged automatically.
	NeedsReview bool
	// Unresolved names the files that aren't anywhere in the archive.
	Unresolved []string
	// Unverified names the signatures the entry claimed and the asset didn't hold
	// up to. They have been dropped from the entry, which is a change nobody should
	// merge without looking at.
	Unverified []string
	// LinkedLibc is the libc the executables need, read from the binaries. It is
	// empty when the asset holds nothing that can be read that way.
	LinkedLibc string
}

// Checksum downloads the asset and returns its SHA256, without extracting it.
//
// It is used for a release published before 2025-06-03, when GitHub started
// reporting a digest for release assets. Those digests are not backfilled, so the
// only way to get a checksum for an older release is to hash the bytes. A
// registry.json without a checksum would defeat the point of the lock file, so this
// isn't optional the way extracting is.
func (v *Verifier) Checksum(ctx context.Context, logger *slog.Logger, version string, asset *generate.Asset) (string, error) {
	url, err := downloadURL(version, asset)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "ar2-checksum")
	if err != nil {
		return "", fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	logger.Debug("downloading an asset to hash it", "url", url)
	return v.download(ctx, url, filepath.Join(dir, assetFileName(url, asset)))
}

// assetFileName is what the downloaded asset is called on disk. An http package has
// no asset name, so the URL supplies one.
func assetFileName(url string, asset *generate.Asset) string {
	if asset.Asset != "" {
		return filepath.Base(asset.Asset)
	}
	return filepath.Base(url)
}

// Verify downloads the asset, extracts it, and resolves its files against the
// archive's actual contents.
func (v *Verifier) Verify(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) (*Result, error) {
	url, err := downloadURL(version, asset)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "ar2-verify")
	if err != nil {
		return nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, assetFileName(url, asset))
	logger.Debug("downloading an asset", "url", url)
	checksum, err := v.download(ctx, url, path)
	if err != nil {
		return nil, err
	}

	// While the file is still here: an entry that says it is signed has to be
	// signed, or what the registry promises isn't what aqua will be able to do.
	var unverified []string
	if v.signatures != nil {
		unverified = v.signatures.Check(ctx, logger, pkgName, version, asset, path)
	}

	dest := filepath.Join(dir, "extracted")
	if err := v.unarchiver.Unarchive(ctx, logger, &unarchive.File{
		Body:     &downloadedFile{path: path},
		Filename: asset.Asset,
		Type:     asset.Format,
	}, dest); err != nil {
		return nil, fmt.Errorf("extract the asset: %w", err)
	}

	result := &Result{Checksum: checksum, Unverified: unverified}
	result.Files, result.NeedsReview, result.Unresolved = resolveFiles(logger, dest, asset.Files)
	// Only Linux has a libc to be linked against, and the files are the resolved
	// ones because that is where the executables actually are.
	if asset.OS == "linux" {
		result.LinkedLibc = linkedLibc(logger, dest, result.Files)
	}
	return result, nil
}

// resolveFiles checks each files[].src against the extracted archive and relocates
// the ones that moved.
//
// Relocating searches by the file's name rather than trying to work out which of the
// extracted files is a command. The name comes from registry.yaml and doesn't change
// when an upstream reorganizes its archive, so looking for it is a far narrower
// guess than picking a binary out of the tree.
func resolveFiles(logger *slog.Logger, dir string, files []*generate.File) ([]*generate.File, bool, []string) {
	var (
		needsReview bool
		unresolved  []string
	)
	out := make([]*generate.File, 0, len(files))
	var index map[string]string
	for _, file := range files {
		src := file.Src
		if src == "" {
			src = file.Name
		}
		if exists(dir, src) {
			out = append(out, file)
			continue
		}
		if index == nil {
			var err error
			if index, err = indexByName(dir); err != nil {
				logger.Warn("failed to index the extracted archive", "error", err.Error())
			}
		}
		found, ok := index[file.Name]
		if !ok {
			found, ok = index[file.Name+".exe"]
		}
		if !ok {
			unresolved = append(unresolved, file.Name)
			needsReview = true
			out = append(out, file)
			continue
		}
		logger.Warn("files[].src doesn't match the archive; relocating it",
			"name", file.Name, "old_src", src, "new_src", found)
		needsReview = true
		out = append(out, &generate.File{Name: file.Name, Src: found})
	}
	return out, needsReview, unresolved
}

// exists reports whether src is present under dir.
// files[].src is always written with forward slashes, so it is split rather than
// used as a path on a system whose separator differs.
func exists(dir, src string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.Join(strings.Split(src, "/")...)))
	return err == nil
}

// indexByName maps each extracted file's base name to its path relative to dir,
// written with forward slashes. A name appearing more than once keeps the shallowest
// path, which is the one an archive normally puts its commands at.
func indexByName(dir string) (map[string]string, error) {
	index := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err //nolint:wrapcheck
		}
		rel = filepath.ToSlash(rel)
		if old, ok := index[d.Name()]; ok && strings.Count(old, "/") <= strings.Count(rel, "/") {
			return nil
		}
		index[d.Name()] = rel
		return nil
	})
	if err != nil {
		return index, fmt.Errorf("walk the extracted archive: %w", err)
	}
	return index, nil
}

// downloadURL returns where the asset is downloaded from.
func downloadURL(version string, asset *generate.Asset) (string, error) {
	if asset.URL != "" {
		return asset.URL, nil
	}
	if asset.RepoOwner == "" || asset.RepoName == "" || asset.Asset == "" {
		return "", errNoDownloadURL
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		asset.RepoOwner, asset.RepoName, version, asset.Asset), nil
}

// download writes the asset to path and returns its SHA256.
// The hash is computed while streaming, so the asset isn't read twice.
func (v *Verifier) download(ctx context.Context, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create a request for the asset: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download the asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download the asset: status code %d", resp.StatusCode)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create a file for the asset: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", fmt.Errorf("write the asset: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
