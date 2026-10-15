package github

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/go-version"
)

// SweepDepth is how many versions of each package a sweep looks at.
//
// A sweep decides whether a package has anything new, and the newest releases are
// where anything new appears once the package's history is in the registry. Asking
// for more costs nothing in rate limit — GitHub charges a GraphQL query by what it
// returns, and a batch of 50 stays at one point either way — but it makes each
// answer bigger for something a sweep never reads.
//
// What a sweep can't see is a release published with an old date, which lands
// further down the list. Those are for the reconciliation that walks the whole
// history to find.
const SweepDepth = 10

// Sweep is what one look at the registry's repositories found, keyed throughout by the
// name that was asked for.
type Sweep struct {
	// Versions is the newest versions of each repository.
	Versions map[string]*Found
	// Names is what each repository is called now. It differs from the key when the
	// repository has been renamed or transferred since the registry recorded it:
	// GitHub answers a query made with the old name and says which name it answered
	// for, so a sweep finds this out without asking anything extra.
	Names map[string]string
	// IDs is each repository's numeric id on GitHub. A name can be taken over by another
	// repository and an id can't, so it is what says the repository is still the one a
	// package was taken over from.
	IDs map[string]int64
	// Reasons says why a repository couldn't be read.
	Reasons map[string]string
}

// Found is what a sweep read of one repository's versions.
type Found struct {
	// Versions are the newest versions, newest first, prereleases among them. A draft
	// isn't public, so it is none of them.
	Versions []string
	// Prereleases are the ones among Versions that are prereleases: a release marked as
	// one, or a tag written as one.
	Prereleases map[string]struct{}
	// Stable says the repository has published something other than a prerelease,
	// whether or not it is among Versions.
	Stable bool
}

// Pick is the versions a package is generated for: all of them when it asks for its
// prereleases or has never published anything else, and the others when it hasn't.
//
// A project that publishes nothing but prereleases has nothing else to install, and
// leaving them out left it with no version at all.
func (f *Found) Pick(prerelease bool) []string {
	if f == nil {
		return nil
	}
	if prerelease || !f.Stable {
		return slices.Clone(f.Versions)
	}
	out := make([]string, 0, len(f.Versions))
	for _, v := range f.Versions {
		if _, ok := f.Prereleases[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}

// Versions returns the newest versions of each repository, newest first, and why any
// of them couldn't be read.
//
// One request covers fifty repositories, and GitHub charges it as one point against
// a budget of its own: a sweep over the whole registry costs about forty-seven
// points out of five thousand an hour, and none of the REST budget that generating
// spends.
//
// Conditional requests would have been the other way to do this, and can't be. The
// REST listing carries each asset's download count, which changes whenever anybody
// downloads one, so its ETag changes with it: the packages a sweep most wants to
// skip are the ones whose ETag never holds. GraphQL doesn't support ETags at all,
// and doesn't need them, because it returns only the fields that were asked for.
func (c *Client) Versions(ctx context.Context, repos []Repo) (*Sweep, error) {
	return c.sweep(ctx, repos, releases, releaseVersions)
}

// releaseVersions reads the versions out of a repository's releases.
//
// Whether the repository has published anything but prereleases is its latest release:
// GitHub's answer leaves out drafts and prereleases, and looks at every release rather
// than at the ones a sweep reads.
func releaseVersions(r *repository) *Found {
	found := &Found{Prereleases: map[string]struct{}{}, Stable: r.LatestRelease != nil}
	if r.Releases == nil {
		return found
	}
	for _, node := range r.Releases.Nodes {
		if node.IsDraft {
			continue
		}
		found.Versions = append(found.Versions, node.TagName)
		if node.IsPrerelease {
			found.Prereleases[node.TagName] = struct{}{}
		}
	}
	return found
}

// Tags returns the newest tags of each repository, newest first.
//
// Some packages are versioned by their tags rather than their releases — flutter
// tags every build and publishes the SDK elsewhere — and aqua-registry says so with
// version_source.
func (c *Client) Tags(ctx context.Context, repos []Repo) (*Sweep, error) {
	return c.sweep(ctx, repos, tags, tagNames)
}

// tagNames reads the names out of a repository's tags.
//
// A package whose versions aqua-registry says are its tags says which of them are versions
// with its version_filter, so none of them is told apart as a prerelease here.
func tagNames(r *repository) *Found {
	return &Found{Versions: tagList(r), Stable: true}
}

// tagList is the names of a repository's tags, newest first.
func tagList(r *repository) []string {
	if r.Refs == nil {
		return nil
	}
	names := make([]string, 0, len(r.Refs.Nodes))
	for _, node := range r.Refs.Nodes {
		names = append(names, node.Name)
	}
	return names
}

// ReleasesOrTags returns the newest versions of each repository from its releases, or from
// its tags when it has no release at all, newest first.
//
// It is for the packages whose versions aren't necessarily releases: one installed with go
// install, downloaded from its own site, or read out of the repository. apache/spark tags
// its versions and publishes none on GitHub, and asked for releases alone it has no
// version at all.
//
// A repository that has releases is read by them even when every one is a draft or a
// prerelease, which says how the project marks a version. A tag carries no such mark, so
// a tag that is written as a prerelease -- v4.3.0-rc2 -- is left out, which is the rule
// releases follow.
func (c *Client) ReleasesOrTags(ctx context.Context, repos []Repo) (*Sweep, error) {
	return c.sweep(ctx, repos, releasesAndTags, releasesOrTags)
}

// releasesOrTags reads the versions out of a repository's releases, or out of its tags
// when it has no release. A tag is a prerelease when it is written as one, and the
// repository has published something else when one of its tags isn't.
func releasesOrTags(r *repository) *Found {
	if r.Releases != nil && len(r.Releases.Nodes) != 0 {
		return releaseVersions(r)
	}
	found := &Found{Versions: tagList(r), Prereleases: map[string]struct{}{}}
	for _, tag := range found.Versions {
		if IsPrerelease(tag) {
			found.Prereleases[tag] = struct{}{}
			continue
		}
		found.Stable = true
	}
	return found
}

// IsPrerelease says whether a tag is written as a prerelease of a version: v4.3.0-rc2.
// A tag that isn't a version at all says nothing about it and isn't one.
func IsPrerelease(tag string) bool {
	v, err := version.NewSemver(tag)
	return err == nil && v.Prerelease() != ""
}

// sweep asks the same thing about every repository, fifty at a time.
func (c *Client) sweep(ctx context.Context, repos []Repo, sel selection, read func(*repository) *Found) (*Sweep, error) {
	out := &Sweep{
		Versions: make(map[string]*Found, len(repos)),
		Names:    map[string]string{},
		IDs:      make(map[string]int64, len(repos)),
		Reasons:  map[string]string{},
	}
	for start := 0; start < len(repos); start += BatchSize {
		end := min(start+BatchSize, len(repos))
		batch := repos[start:end]
		record := func(repo Repo, r *repository) {
			out.Versions[repo.String()] = read(r)
			if r.DatabaseID != 0 {
				out.IDs[repo.String()] = r.DatabaseID
			}
			if renamed(repo.String(), r.NameWithOwner) {
				out.Names[repo.String()] = r.NameWithOwner
			}
		}
		if err := c.fetch(ctx, batch, sel, record, out.Reasons); err != nil {
			// One failing batch must not throw away what the others answered: a
			// package nothing is known about is looked at rather than skipped, which
			// is the safe way round.
			for _, repo := range batch {
				out.Reasons[repo.String()] = err.Error()
			}
		}
	}
	return out, nil
}

func releases(ownerVar, nameVar string) string {
	return fmt.Sprintf(
		"repository(owner: $%s, name: $%s) { nameWithOwner databaseId latestRelease { tagName } releases(first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName isDraft isPrerelease } } }",
		ownerVar, nameVar, SweepDepth)
}

func releasesAndTags(ownerVar, nameVar string) string {
	return fmt.Sprintf(
		"repository(owner: $%s, name: $%s) { nameWithOwner databaseId latestRelease { tagName } releases(first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName isDraft isPrerelease } } refs(refPrefix: \"refs/tags/\", first: %d, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) { nodes { name } } }",
		ownerVar, nameVar, SweepDepth, SweepDepth)
}

func tags(ownerVar, nameVar string) string {
	return fmt.Sprintf(
		"repository(owner: $%s, name: $%s) { nameWithOwner databaseId refs(refPrefix: \"refs/tags/\", first: %d, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) { nodes { name } } }",
		ownerVar, nameVar, SweepDepth)
}

// renamed says GitHub answered for a repository under another name.
//
// Case doesn't count. GitHub answers a query made in any case and replies with the owner
// and the repository as they are spelled, so Arriven/db1000n comes back as
// arriven/db1000n -- the same repository, written the way its owner writes it now.
// Treating that as a rename would move the package to a name that differs from the old one
// only in case, and leave the old one behind as an alias of it, which says nothing anybody
// needs to know.
func renamed(asked, answered string) bool {
	return answered != "" && !strings.EqualFold(asked, answered)
}

// RepoIDs returns each repository's numeric id, fifty repositories to a request.
func (c *Client) RepoIDs(ctx context.Context, repos []Repo) (map[string]int64, error) {
	out := make(map[string]int64, len(repos))
	reasons := map[string]string{}
	for start := 0; start < len(repos); start += BatchSize {
		batch := repos[start:min(start+BatchSize, len(repos))]
		record := func(repo Repo, r *repository) {
			if r.DatabaseID != 0 {
				out[repo.String()] = r.DatabaseID
			}
		}
		if err := c.fetch(ctx, batch, repoID, record, reasons); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func repoID(ownerVar, nameVar string) string {
	return fmt.Sprintf("repository(owner: $%s, name: $%s) { databaseId }", ownerVar, nameVar)
}
