package github

import (
	"context"
	"fmt"
	"strings"
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
	// Versions is the newest versions of each repository, newest first.
	Versions map[string][]string
	// Names is what each repository is called now. It differs from the key when the
	// repository has been renamed or transferred since the registry recorded it:
	// GitHub answers a query made with the old name and says which name it answered
	// for, so a sweep finds this out without asking anything extra.
	Names map[string]string
	// Reasons says why a repository couldn't be read.
	Reasons map[string]string
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
// A draft isn't public and a prerelease isn't what a registry offers, which is the
// same rule the REST path applies.
func releaseVersions(r *repository) []string {
	if r.Releases == nil {
		return nil
	}
	versions := make([]string, 0, len(r.Releases.Nodes))
	for _, node := range r.Releases.Nodes {
		if node.IsDraft || node.IsPrerelease {
			continue
		}
		versions = append(versions, node.TagName)
	}
	return versions
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
func tagNames(r *repository) []string {
	if r.Refs == nil {
		return nil
	}
	names := make([]string, 0, len(r.Refs.Nodes))
	for _, node := range r.Refs.Nodes {
		names = append(names, node.Name)
	}
	return names
}

// sweep asks the same thing about every repository, fifty at a time.
func (c *Client) sweep(ctx context.Context, repos []Repo, sel selection, read func(*repository) []string) (*Sweep, error) {
	out := &Sweep{
		Versions: make(map[string][]string, len(repos)),
		Names:    map[string]string{},
		Reasons:  map[string]string{},
	}
	for start := 0; start < len(repos); start += BatchSize {
		end := min(start+BatchSize, len(repos))
		batch := repos[start:end]
		record := func(repo Repo, r *repository) {
			out.Versions[repo.String()] = read(r)
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
		"repository(owner: $%s, name: $%s) { nameWithOwner releases(first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName isDraft isPrerelease } } }",
		ownerVar, nameVar, SweepDepth)
}

func tags(ownerVar, nameVar string) string {
	return fmt.Sprintf(
		"repository(owner: $%s, name: $%s) { nameWithOwner refs(refPrefix: \"refs/tags/\", first: %d, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) { nodes { name } } }",
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
