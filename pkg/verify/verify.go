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
// carried over unchanged. One whose file is elsewhere in the archive has moved rather than
// changed -- the directory an upstream adds around what it packs, or drops -- and the one
// executable of the name is the file wherever it is, so recording where it is now says
// what the definition said, and that merges on its own too. What the archive can't answer
// -- no executable of the name, or several -- leaves the environment out, for the
// definition to say.
package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
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
	// downloadLimit is how long one download may take.
	downloadLimit time.Duration
}

// Signatures verifies an asset against the signatures its entry claims and reports
// whether they all held.
type Signatures interface {
	Check(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset, path string) error
}

// New creates a Verifier.
//
// The unarchiver gets an executor, so dmg and pkg assets can be extracted where the
// tools that open them exist. Whether they do is asked before extracting rather than
// assumed from the format: the same code runs on a macOS laptop and a Linux runner,
// and only one of them can open a dmg.
func New(httpClient *http.Client, signatures Signatures) *Verifier {
	return &Verifier{
		httpClient:    httpClient,
		unarchiver:    unarchive.New(osexec.New()),
		signatures:    signatures,
		downloadLimit: defaultDownloadLimit,
	}
}

// defaultDownloadLimit is how long one download may take before it is given up on.
//
// A server can serve slowly enough that a download goes on making progress for longer than
// a run lasts. apache/spark's 400 MB archive comes from archive.apache.org, and the run
// that reached it was stopped at its time limit before it saved what it had done, so the
// next run started at apache/spark again.
const defaultDownloadLimit = 5 * time.Minute

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
	// NeedsReview is set when a file couldn't be resolved. A pull request carrying such a
	// result must not be merged automatically.
	NeedsReview bool
	// Unresolved names the files the archive holds no executable of, or several of.
	Unresolved []string
	// Holds is what the archive does hold, when something couldn't be resolved. It is the
	// one thing that says what the definition should have said, and the archive is gone as
	// soon as the run ends.
	Holds []string
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

// VerifySignatures downloads the asset and checks the signatures its entry claims, without
// extracting it, and returns the asset's checksum. It is for an asset this machine can't
// open.
func (v *Verifier) VerifySignatures(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) (string, error) {
	url, err := downloadURL(version, asset)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "ar2-signatures")
	if err != nil {
		return "", fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, assetFileName(url, asset))
	logger.Debug("downloading an asset to check its signatures", "url", url)
	checksum, err := v.download(ctx, url, path)
	if err != nil {
		return "", err
	}
	if err := v.signatures.Check(ctx, logger, pkgName, version, asset, path); err != nil {
		return "", err //nolint:wrapcheck // it already names the signature and why
	}
	return checksum, nil
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
	if v.signatures != nil {
		if err := v.signatures.Check(ctx, logger, pkgName, version, asset, path); err != nil {
			return nil, err //nolint:wrapcheck // it already names the signature and why
		}
	}

	dest := filepath.Join(dir, "extracted")
	if err := v.unarchiver.Unarchive(ctx, logger, &unarchive.File{
		Body:     &downloadedFile{path: path},
		Filename: asset.Asset,
		Type:     asset.Format,
	}, dest); err != nil {
		return nil, fmt.Errorf("extract the asset: %w", err)
	}

	result := &Result{Checksum: checksum}
	result.Files, result.NeedsReview, result.Unresolved, result.Holds = resolveFiles(logger, dest, asset.OS, windowsExtOf(asset), asset.Files)
	if err := checkArm64(dest, asset, result.Files); err != nil {
		return nil, err
	}
	// Only Linux has a libc to be linked against, and the files are the resolved
	// ones because that is where the executables actually are.
	if asset.OS == "linux" {
		result.LinkedLibc = linkedLibc(logger, dest, result.Files)
	}
	return result, nil
}

// resolver answers where one files[] entry is in an extracted archive. The index of
// the archive's contents is built the first time something has to be looked up in it,
// since an archive whose entries are all where the definition says need never be
// walked.
type resolver struct {
	logger *slog.Logger
	dir    string
	goos   string
	// ext is the extension aqua gives the package's executables on Windows.
	ext   string
	index map[string][]*found
}

// found is one file of a name in the extracted archive.
type found struct {
	// Path is where it is, relative to the archive's root.
	Path string
}

// resolve returns the entry to record, or nil when the archive doesn't hold the file. When
// it holds several files the entry could mean, it returns nil and their paths.
//
// The definition's src is kept when the archive holds it. Otherwise the file is looked for
// by its name, which comes from the definition and doesn't change when an upstream
// reorganizes its archive, among the files that can be run: what tells the command from a
// file named after it, a completion script or a man page, is that it is an executable, and
// that is read from the file itself rather than from a mode the archive may not carry. One
// such file is the command wherever it is, so recording where it is now says what the
// definition said. More than one is an archive saying the name twice with nothing to tell
// them apart.
func (r *resolver) resolve(file *generate.File) (*generate.File, []string) {
	src := file.Src
	if src == "" {
		src = file.Name
	}
	if exists(r.dir, src) {
		return file, nil
	}
	// aqua completes the Windows extension when it resolves files[].src, and at
	// install time renames the extracted file to match. The archive holds the name
	// without it, and that is what the entry has to say: nothing has renamed anything
	// by the time the next reader looks for it.
	if bare, ok := withoutWindowsExt(r.goos, r.ext, src); ok && exists(r.dir, bare) {
		return &generate.File{Name: file.Name, Src: bare}, nil
	}
	found, several := r.byName(file.Name)
	if found == "" {
		return nil, several
	}
	r.logger.Info("files[].src is elsewhere in the archive",
		"name", file.Name, "old_src", src, "new_src", found)
	return &generate.File{Name: file.Name, Src: found}, nil
}

// byName is the one executable in the archive carrying the name, with or without the Windows
// extension, or, when there are several, "" and their paths.
//
// On Windows the extension is looked for first. git-bug's archive holds git-bug.exe beside
// a completion script called git-bug, and the name alone finds the script.
//
// A release shipping a completion script named after its command is the ordinary case --
// CMake's archive holds bin/cmake and share/bash-completion/completions/cmake -- and the
// script is no executable, so the archive answers which is which rather than leaving it to
// a guess about paths.
func (r *resolver) byName(name string) (string, []string) {
	if r.index == nil {
		index, err := indexByName(r.dir)
		if err != nil {
			r.logger.Warn("failed to index the extracted archive", "error", err.Error())
		}
		r.index = index
	}
	for _, candidate := range r.candidates(name) {
		var runnable []string
		for _, f := range r.index[candidate] {
			if r.runnable(f.Path) {
				runnable = append(runnable, f.Path)
			}
		}
		switch len(runnable) {
		case 0:
			continue
		case 1:
			return runnable[0], nil
		default:
			return "", runnable
		}
	}
	return "", nil
}

// runnable reports whether the file at the path, relative to the archive's root, can be run:
// an ELF, Mach-O or PE executable, a script naming its interpreter, or on Windows a script
// of an extension Windows runs.
func (r *resolver) runnable(rel string) bool {
	if r.goos == osWindows {
		for _, ext := range windowsScriptExts {
			if strings.HasSuffix(strings.ToLower(rel), ext) {
				return true
			}
		}
	}
	return executableHeader(filepath.Join(r.dir, filepath.FromSlash(rel)))
}

// windowsScriptExts are the extensions of the scripts Windows runs, which carry no header to
// read: sass/dart-sass runs from sass.bat.
var windowsScriptExts = []string{".bat", ".cmd", ".ps1"} //nolint:gochecknoglobals

// executableHeaders are how the files that can be run begin: ELF, PE, Mach-O in either byte
// order and width, a universal Mach-O, and a script naming its interpreter.
var executableHeaders = [][]byte{ //nolint:gochecknoglobals
	{0x7f, 'E', 'L', 'F'},
	{'M', 'Z'},
	{0xfe, 0xed, 0xfa, 0xce},
	{0xfe, 0xed, 0xfa, 0xcf},
	{0xce, 0xfa, 0xed, 0xfe},
	{0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	{'#', '!'},
}

// executableHeader reports whether the file begins the way an executable does.
func executableHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4) //nolint:mnd // the longest header
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	for _, h := range executableHeaders {
		if bytes.HasPrefix(head, h) {
			return true
		}
	}
	return false
}

// candidates is the names to look for, in the order the environment makes likely.
func (r *resolver) candidates(name string) []string {
	if r.goos == osWindows {
		return []string{name + r.ext, name}
	}
	return []string{name, name + r.ext}
}

// resolveFiles checks each files[].src against the extracted archive and relocates
// the ones that moved.
//
// Relocating searches by the file's name rather than trying to work out which of the
// extracted files is a command. The name comes from registry.yaml and doesn't change
// when an upstream reorganizes its archive, so looking for it is a far narrower
// guess than picking a binary out of the tree.
//
// A file it can't resolve is said in unresolved, as its name, and with the paths when the
// archive holds several executables of the name.
func resolveFiles(logger *slog.Logger, dir, goos, ext string, files []*generate.File) ([]*generate.File, bool, []string, []string) {
	var unresolved []string
	r := &resolver{logger: logger, dir: dir, goos: goos, ext: ext}
	out := make([]*generate.File, 0, len(files))
	for _, file := range files {
		resolved, several := r.resolve(file)
		if resolved == nil {
			unresolved = append(unresolved, unresolvedName(file.Name, several))
			out = append(out, file)
			continue
		}
		out = append(out, resolved)
	}
	if len(unresolved) == 0 {
		return out, false, nil, nil
	}
	// What the archive does hold, because that is what whoever writes the definition
	// needs and there is no other way to see it: the asset is downloaded by a run and
	// gone by the time anybody reads about it.
	return out, true, unresolved, holds(r.index)
}

// unresolvedName is how a file that couldn't be resolved is said: its name, and the
// executables of that name when the archive holds several.
func unresolvedName(name string, several []string) string {
	if len(several) == 0 {
		return name
	}
	return name + " (several executables of this name: " + strings.Join(several, ", ") + ")"
}

// holdsLimit is how many of an archive's files are worth saying. A release that packs a
// whole tree has nothing to tell a reader after the first few names.
const holdsLimit = 20

// holds is what the archive holds, as paths, in order and capped.
func holds(index map[string][]*found) []string {
	paths := make([]string, 0, len(index))
	for _, entries := range index {
		for _, entry := range entries {
			paths = append(paths, entry.Path)
		}
	}
	sort.Strings(paths)
	if len(paths) > holdsLimit {
		return append(paths[:holdsLimit:holdsLimit], fmt.Sprintf("and %d more", len(paths)-holdsLimit))
	}
	return paths
}

// osWindows is the OS whose executables carry an extension.
const osWindows = "windows"

// windowsExt is the extension aqua gives an executable on Windows.
const windowsExt = ".exe"

// scriptExt is the extension aqua gives a file of a repository on Windows, which is a
// script rather than a build.
const scriptExt = ".sh"

// windowsExtOf is the extension aqua completes files[].src with on Windows for the asset:
// .sh for a file of the repository or its archive, and .exe for the rest. A definition can
// name another with windows_ext, but the entry generated here carries no trace of it.
func windowsExtOf(asset *generate.Asset) string {
	switch asset.Type {
	case aquaregistry.PkgInfoTypeGitHubContent, aquaregistry.PkgInfoTypeGitHubArchive:
		return scriptExt
	}
	return windowsExt
}

// withoutWindowsExt returns src with the Windows extension ext removed, and whether it had
// it.
func withoutWindowsExt(goos, ext, src string) (string, bool) {
	if goos != osWindows {
		return src, false
	}
	bare := strings.TrimSuffix(src, ext)
	return bare, bare != src
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
func indexByName(dir string) (map[string][]*found, error) {
	index := map[string][]*found{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err //nolint:wrapcheck
		}
		index[d.Name()] = append(index[d.Name()], &found{Path: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return index, fmt.Errorf("walk the extracted archive: %w", err)
	}
	// The shallowest path first, which is where an archive normally puts the command
	// rather than a copy of it or a document about it.
	for _, paths := range index {
		slices.SortFunc(paths, func(a, b *found) int {
			if n := strings.Count(a.Path, "/") - strings.Count(b.Path, "/"); n != 0 {
				return n
			}
			return strings.Compare(a.Path, b.Path)
		})
	}
	return index, nil
}

// downloadURL returns where the asset is downloaded from.
//
// The host is the entry's for a release on a forge instance, and github.com for the rest.
// The Gitea family serves a release asset at the path GitHub does, so for those the entry
// needs to say nothing but which instance; GitLab serves it at its permanent release
// link, which is a path of its own.
//
// A file of the repository and the repository's archive aren't release assets, and are
// downloaded from where aqua downloads them.
func downloadURL(version string, asset *generate.Asset) (string, error) {
	if asset.URL != "" {
		return asset.URL, nil
	}
	if u, ok, err := repositoryURL(version, asset); ok {
		return u, err
	}
	if asset.RepoOwner == "" || asset.RepoName == "" || asset.Asset == "" {
		return "", errNoDownloadURL
	}
	host := asset.Host
	if host == "" {
		host = "github.com"
	}
	if asset.Type == aquaregistry.PkgInfoTypeGitLabRelease {
		// The namespace and the asset are read as paths -- a project in subgroups
		// and an asset named by the file path its release link was created with --
		// so their separators are kept and only what is inside a segment is
		// escaped. The tag is one segment: a version holding a slash names the
		// release rather than a path.
		return fmt.Sprintf("https://%s/%s/%s/-/releases/%s/downloads/%s",
			host, escapePath(asset.RepoOwner), url.PathEscape(asset.RepoName),
			url.PathEscape(version), escapePath(asset.Asset)), nil
	}
	return fmt.Sprintf("https://%s/%s/%s/releases/download/%s/%s",
		host, asset.RepoOwner, asset.RepoName, version, asset.Asset), nil
}

// repositoryURL returns where a file of the repository or the repository's archive is
// downloaded from, and false for any other type.
func repositoryURL(version string, asset *generate.Asset) (string, bool, error) {
	switch asset.Type {
	case aquaregistry.PkgInfoTypeGitHubContent:
		if asset.RepoOwner == "" || asset.RepoName == "" || asset.Path == "" {
			return "", true, errNoDownloadURL
		}
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
			asset.RepoOwner, asset.RepoName, url.PathEscape(version), escapePath(asset.Path)), true, nil
	case aquaregistry.PkgInfoTypeGitHubArchive:
		if asset.RepoOwner == "" || asset.RepoName == "" {
			return "", true, errNoDownloadURL
		}
		return fmt.Sprintf("https://github.com/%s/%s/archive/refs/tags/%s.tar.gz",
			asset.RepoOwner, asset.RepoName, url.PathEscape(version)), true, nil
	}
	return "", false, nil
}

// escapePath escapes each segment of a path, keeping the separators between them.
func escapePath(s string) string {
	segments := strings.Split(s, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// download writes the asset to path and returns its SHA256.
// The hash is computed while streaming, so the asset isn't read twice.
func (v *Verifier) download(ctx context.Context, url, path string) (string, error) {
	limited, cancel := context.WithTimeout(ctx, v.downloadLimit)
	defer cancel()
	checksum, err := v.fetch(limited, url, path)
	if err != nil && ctx.Err() == nil && errors.Is(limited.Err(), context.DeadlineExceeded) {
		return "", &SlowError{URL: url, Limit: v.downloadLimit}
	}
	return checksum, err
}

// fetch writes the asset to path and returns its SHA256.
func (v *Verifier) fetch(ctx context.Context, url, path string) (string, error) {
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
		return "", fmt.Errorf("download the asset: %w", &StatusError{Code: resp.StatusCode})
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
