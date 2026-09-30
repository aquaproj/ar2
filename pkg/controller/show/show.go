// Package show answers what the registry knows about one package.
//
// A package's branch is named after an identifier rather than after the package, so
// neither can be read off the other: a name in a pull request title, a branch in a log,
// an identifier in a lock file -- each one leaves the others to be looked up. The
// catalogue holds all of it, and this is the lookup.
package show

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
)

// Registry is the catalogue this reads.
type Registry interface {
	Index(ctx context.Context, ref string) (*aquag2.Index, error)
}

// Controller answers about one package.
type Controller struct {
	g2         Registry
	baseBranch string
}

// New creates a Controller.
func New(registry Registry, baseBranch string) *Controller {
	return &Controller{g2: registry, baseBranch: baseBranch}
}

// Show writes what the catalogue says about the package the query names.
//
// The query is a name -- the one the package has now or one it used to have -- or the
// identifier, because the whole point of asking is that only one of them is in hand.
func (c *Controller) Show(ctx context.Context, w io.Writer, query string) error {
	index, err := c.g2.Index(ctx, c.baseBranch)
	if err != nil {
		return fmt.Errorf("read the catalogue: %w", err)
	}
	pkg, found := find(index, query)
	if !found {
		return fmt.Errorf("%w: %s", errNotFound, query)
	}
	write(w, pkg)
	return nil
}

// find looks for the package by each of the things it can be asked for, in the order that
// answers the most asks with the fewest surprises: the name it has, the identifier, then
// the names it used to have.
func find(index *aquag2.Index, query string) (*aquag2.IndexPackage, bool) {
	var byAlias *aquag2.IndexPackage
	for _, pkg := range index.Packages {
		if pkg == nil {
			continue
		}
		switch {
		case pkg.Name == query, pkg.ID != "" && pkg.ID == query:
			return pkg, true
		case byAlias == nil && slices.Contains(pkg.Aliases, query):
			byAlias = pkg
		}
	}
	return byAlias, byAlias != nil
}

// write prints the entry, one fact per line, leaving out what the entry doesn't say.
func write(w io.Writer, pkg *aquag2.IndexPackage) {
	fmt.Fprintf(w, "package:     %s\n", pkg.Name)
	if pkg.ID != "" {
		fmt.Fprintf(w, "id:          %s (%s)\n", pkg.ID, minted(pkg.ID))
		fmt.Fprintf(w, "branch:      %s\n", g2.IDBranchName(pkg.ID))
	} else {
		// A package taken over before the registry minted identifiers. The next
		// reconciliation gives it one.
		fmt.Fprintf(w, "id:          none yet\n")
		fmt.Fprintf(w, "branch:      %s\n", g2.IDBranchName(pkg.ID))
	}
	if pkg.Description != "" {
		fmt.Fprintf(w, "description: %s\n", pkg.Description)
	}
	if pkg.Link != "" {
		fmt.Fprintf(w, "link:        %s\n", pkg.Link)
	}
	if len(pkg.Aliases) > 0 {
		fmt.Fprintf(w, "aliases:     %s\n", strings.Join(pkg.Aliases, ", "))
	}
	if len(pkg.SearchWords) > 0 {
		fmt.Fprintf(w, "search:      %s\n", strings.Join(pkg.SearchWords, ", "))
	}
}

// minted reads the identifier back as the time it was minted, which is the one thing it
// says about itself. An identifier that isn't a time is shown as it is rather than
// guessed at.
func minted(id string) string {
	sec, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "not a time"
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
