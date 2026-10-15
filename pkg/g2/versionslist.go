package g2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// commitVersionsList writes versions.json onto a commit holding versions it doesn't list
// yet, and returns the commit that does. A package left with no versions loses its list.
//
// The list is a reading aid: whether a version is served is what the versions directory
// says, and the list is how a reader learns that, when each was published and whether the
// file it has is the one served, in one request instead of one per version.
func (c *Client) commitVersionsList(ctx context.Context, logger *slog.Logger, dir, parent, commit string, files []*File) (string, error) {
	listPath := dir + "/" + aquag2.VersionsFileName
	tree, err := c.versionsTree(ctx, commit, dir)
	if err != nil {
		return "", err
	}
	if tree == "" {
		held, err := c.File(ctx, parent, listPath)
		if err != nil {
			return "", err
		}
		if held == "" {
			return commit, nil
		}
		return c.commitTree(ctx, commit, "chore: remove the list of versions",
			[]*gogithub.TreeEntry{deletedEntry(listPath)})
	}
	list, err := c.versionsList(ctx, logger, dir, parent, commit, tree, files)
	if err != nil {
		return "", err
	}
	content, err := marshalVersions(list)
	if err != nil {
		return "", err
	}
	return c.commitTree(ctx, commit, versionsMessage(len(list.Versions)), []*gogithub.TreeEntry{{
		Path:    new(listPath),
		Mode:    new(blobMode),
		Type:    new(blobType),
		Content: new(content),
	}})
}

// versionsList is the list of the versions the commit holds, made from tree, the sha of
// its versions directory.
//
// The list the parent holds is the starting point when it is still the list of what the
// parent holds -- its source is the parent's versions directory -- and then only the
// versions this commit writes or takes out are read. Otherwise every version is read from
// the commit, which is a request per version and what a package taken over in this commit,
// or one whose list fell behind, costs once.
func (c *Client) versionsList(ctx context.Context, logger *slog.Logger, dir, parent, commit, tree string, files []*File) (*aquag2.Versions, error) {
	held, err := c.heldVersions(ctx, dir, parent)
	if err != nil {
		return nil, err
	}
	written := map[string]*File{}
	for _, file := range files {
		if version, ok := versionOfPath(file.Path); ok {
			written[version] = file
		}
	}

	var entries map[string]*aquag2.Version
	if held != nil {
		entries = updatedEntries(logger, held, written)
	} else {
		entries, err = c.readEntries(ctx, logger, dir, commit, written)
		if err != nil {
			return nil, err
		}
	}

	out := &aquag2.Versions{Source: tree, Versions: make([]*aquag2.Version, 0, len(entries))}
	for _, v := range entries {
		out.Versions = append(out.Versions, v)
	}
	out.Sort()
	return out, nil
}

// updatedEntries is the parent's list with the versions this commit writes or takes out.
func updatedEntries(logger *slog.Logger, held *aquag2.Versions, written map[string]*File) map[string]*aquag2.Version {
	entries := make(map[string]*aquag2.Version, len(held.Versions)+len(written))
	for _, v := range held.Versions {
		if v != nil {
			entries[v.Version] = v
		}
	}
	for version, file := range written {
		if file.Deleted {
			delete(entries, version)
			continue
		}
		entries[version] = versionEntry(logger, version, file.Content)
	}
	return entries
}

// readEntries is every version the commit holds, each read from its file: from what this
// commit writes when it writes it, and from the commit otherwise.
func (c *Client) readEntries(ctx context.Context, logger *slog.Logger, dir, commit string, written map[string]*File) (map[string]*aquag2.Version, error) {
	versions, err := c.versionsIn(ctx, logger, commit, dir)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]*aquag2.Version, len(versions))
	for version := range versions {
		if file, ok := written[version]; ok && !file.Deleted {
			entries[version] = versionEntry(logger, version, file.Content)
			continue
		}
		content, err := c.File(ctx, commit, dir+"/"+aquag2.Path(version))
		if err != nil {
			return nil, fmt.Errorf("read the registry.json of %s: %w", version, err)
		}
		entries[version] = versionEntry(logger, version, content)
	}
	return entries, nil
}

// heldVersions is the list the parent holds when it is the list of the parent's versions,
// and nil when there is none or it has fallen behind.
func (c *Client) heldVersions(ctx context.Context, dir, parent string) (*aquag2.Versions, error) {
	content, err := c.File(ctx, parent, dir+"/"+aquag2.VersionsFileName)
	if err != nil {
		return nil, err
	}
	if content == "" {
		return nil, nil //nolint:nilnil // no list, which isn't a failure
	}
	list, err := aquag2.ReadVersions(strings.NewReader(content))
	if err != nil || list.Source == "" {
		return nil, nil //nolint:nilnil,nilerr // a list that can't be read is made again
	}
	tree, err := c.versionsTree(ctx, parent, dir)
	if err != nil {
		return nil, err
	}
	if list.Source != tree {
		return nil, nil //nolint:nilnil // a list behind its directory is made again
	}
	return list, nil
}

// versionOfPath is the version whose registry.json a path from the package's directory is,
// and false for any other file.
func versionOfPath(p string) (string, bool) {
	dir, file := path.Split(p)
	if file != aquag2.FileName {
		return "", false
	}
	escaped, ok := strings.CutPrefix(strings.TrimSuffix(dir, "/"), aquag2.VersionDir+"/")
	if !ok || strings.Contains(escaped, "/") {
		return "", false
	}
	return aquag2.DecodeVersion(escaped)
}

// versionEntry is what the list says about one version, read from the file the registry
// serves for it.
func versionEntry(logger *slog.Logger, version, content string) *aquag2.Version {
	entry := &aquag2.Version{Version: version}
	if content == "" {
		// The directory is there and the file isn't, which is a version the registry
		// doesn't serve. It is still a version the package holds, so it is listed, and
		// what can't be read about it is left out.
		logger.Warn("the version holds no registry.json", "version", version)
		return entry
	}
	entry.Digest = digest(content)
	reg := &aquag2.Registry{}
	if err := json.Unmarshal([]byte(content), reg); err != nil {
		logger.Warn("failed to read a registry.json", "version", version, "error", err.Error())
		return entry
	}
	entry.PublishedAt = reg.PublishedAt
	return entry
}

// digest is the SHA-256 of the file the registry serves, as a reader of the list would
// compute it over the bytes it downloaded.
func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// marshalVersions renders the list indented. It is read by people in a pull request as well
// as by programs, it is one file per package rather than per version, and git packs the
// whitespace away.
func marshalVersions(versions *aquag2.Versions) (string, error) {
	b, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", aquag2.VersionsFileName, err)
	}
	return string(b) + "\n", nil
}

// versionsMessage is what the commit writing the list says.
func versionsMessage(versions int) string {
	if versions == 1 {
		return "chore: list the version the registry holds"
	}
	return fmt.Sprintf("chore: list the %d versions the registry holds", versions)
}

// RenderVersionsList renders versions.json for the files a package's versions directory
// holds -- each version's registry.json, keyed by the version -- made from the tree whose
// sha is source. It is what CommitPackage writes, for a caller that has every file already.
func RenderVersionsList(logger *slog.Logger, source string, files map[string]string) (string, error) {
	out := &aquag2.Versions{Source: source, Versions: make([]*aquag2.Version, 0, len(files))}
	for version, content := range files {
		out.Versions = append(out.Versions, versionEntry(logger, version, content))
	}
	out.Sort()
	return marshalVersions(out)
}

// VersionsBranch is where an update to the packages' lists of versions is committed.
//
// One fixed branch, as the catalogue has: a list is made from what the default branch holds,
// so two runs writing to it produce what the second would have alone, and a run whose pull
// request is still open adds to it instead of opening another.
const VersionsBranch = HeadBranchPrefix + "versions"

// VersionsListFile is the versions.json the package whose id this is should hold, made from
// the versions directory on ref, or nil when what ref holds already says it.
//
// It is for a list that fell behind: a pull request adding one version leaves the list alone,
// since two of them for one package would conflict on it, and the list is brought up to date
// after they merge. The list ref holds is the starting point, and only the versions it lacks
// are read and the ones gone taken out; a list that can't be read is made again from every
// version.
func (c *Client) VersionsListFile(ctx context.Context, logger *slog.Logger, ref, id string) (*File, error) {
	dir := PackageDir(id)
	listPath := dir + "/" + aquag2.VersionsFileName
	tree, err := c.versionsTree(ctx, ref, dir)
	if err != nil {
		return nil, err
	}
	content, err := c.File(ctx, ref, listPath)
	if err != nil {
		return nil, err
	}
	if tree == "" {
		if content == "" {
			return nil, nil //nolint:nilnil // no versions and no list, which is in step
		}
		return &File{Path: listPath, Deleted: true}, nil
	}
	held := readList(content)
	if held != nil && held.Source == tree {
		return nil, nil //nolint:nilnil // the list is of what the directory holds
	}
	entries, err := c.listedEntries(ctx, logger, ref, dir, held)
	if err != nil {
		return nil, err
	}
	out := &aquag2.Versions{Source: tree, Versions: make([]*aquag2.Version, 0, len(entries))}
	for _, v := range entries {
		out.Versions = append(out.Versions, v)
	}
	out.Sort()
	rendered, err := marshalVersions(out)
	if err != nil {
		return nil, err
	}
	return &File{Path: listPath, Content: rendered}, nil
}

// listedEntries is what the list should say about each version the directory on ref holds:
// what held says about the ones it lists, and what each other one's file says.
func (c *Client) listedEntries(ctx context.Context, logger *slog.Logger, ref, dir string, held *aquag2.Versions) (map[string]*aquag2.Version, error) {
	versions, err := c.versionsIn(ctx, logger, ref, dir)
	if err != nil {
		return nil, err
	}
	entries := map[string]*aquag2.Version{}
	if held != nil {
		for _, v := range held.Versions {
			if v == nil {
				continue
			}
			if _, ok := versions[v.Version]; ok {
				entries[v.Version] = v
			}
		}
	}
	for version := range versions {
		if _, ok := entries[version]; ok {
			continue
		}
		file, err := c.File(ctx, ref, dir+"/"+aquag2.Path(version))
		if err != nil {
			return nil, fmt.Errorf("read the registry.json of %s: %w", version, err)
		}
		entries[version] = versionEntry(logger, version, file)
	}
	return entries, nil
}

// readList reads a list of versions, or nil when there is none or it can't be read.
func readList(content string) *aquag2.Versions {
	if content == "" {
		return nil
	}
	list, err := aquag2.ReadVersions(strings.NewReader(content))
	if err != nil {
		return nil
	}
	return list
}

// VersionsPullRequest returns the open pull request updating the lists of versions, or nil
// when there is none.
func (c *Client) VersionsPullRequest(ctx context.Context) (*gogithub.PullRequest, error) {
	return c.pullRequestFrom(ctx, VersionsBranch)
}

// CreateVersionsPullRequest opens a pull request updating the lists of versions.
func (c *Client) CreateVersionsPullRequest(ctx context.Context, logger *slog.Logger, base, title, body string) (*gogithub.PullRequest, error) {
	return c.CreatePullRequestFrom(ctx, logger, VersionsBranch, base, title, body)
}

// VersionsLists is what reading every package's list of versions asks of the repository.
type VersionsLists interface {
	// Subtrees is the directories two levels under the tree an expression names.
	Subtrees(ctx context.Context, expression string) ([]string, error)
	// OIDs is the sha of the object each expression names, keyed by the expression.
	OIDs(ctx context.Context, expressions []string) (map[string]string, error)
	// Blobs is the text of the file each expression names, keyed by the expression.
	Blobs(ctx context.Context, logger *slog.Logger, expressions []string) (map[string]string, error)
}

// StaleVersionsLists returns the ids of the packages whose versions.json on the default
// branch isn't the list of what their versions directory holds: one whose source isn't the
// directory's sha, which is also one with versions and no list, one with a list and no
// versions, and one whose list can't be read.
//
// It is asked of every package, from what the default branch holds rather than from what
// changed, so nothing has to be remembered between runs: whichever runs next finds what
// a run that didn't happen would have. Every package costs two objects, read a hundred to a
// query, which is what reading the identities costs.
func StaleVersionsLists(ctx context.Context, logger *slog.Logger, repo VersionsLists) ([]string, error) {
	dirs, err := repo.Subtrees(ctx, DefaultBranch+":"+PackagesDir)
	if err != nil {
		return nil, fmt.Errorf("list the packages: %w", err)
	}
	ids := packageIDs(logger, dirs)
	trees := make([]string, len(ids))
	lists := make([]string, len(ids))
	for i, id := range ids {
		dir := DefaultBranch + ":" + PackageDir(id)
		trees[i] = dir + "/" + aquag2.VersionDir
		lists[i] = dir + "/" + aquag2.VersionsFileName
	}
	shas, err := repo.OIDs(ctx, trees)
	if err != nil {
		return nil, fmt.Errorf("read the versions directories: %w", err)
	}
	texts, err := repo.Blobs(ctx, logger, lists)
	if err != nil {
		return nil, fmt.Errorf("read the lists of versions: %w", err)
	}
	var stale []string
	for i, id := range ids {
		source := ""
		if text, ok := texts[lists[i]]; ok {
			source = "unreadable"
			if list := readList(text); list != nil {
				source = list.Source
			}
		}
		if shas[trees[i]] != source {
			stale = append(stale, id)
		}
	}
	return stale, nil
}
