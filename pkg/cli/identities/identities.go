// Package identities tells a registry client what each package's branch is named after.
//
// A branch is named after the package's id, so nothing about a branch's name says which
// package it holds. Every command that reads or writes one starts by reading the table out
// of the branches themselves, which is one query per hundred of them.
package identities

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/github"
)

// Read tells the registry what each package's branch is named after, and returns the
// definition each branch holds along with the table.
//
// The definitions come back because they are what the table was read out of: a command that
// works through every definition would otherwise read the same pages again.
func Read(ctx context.Context, logger *slog.Logger, registry *g2.Client, httpClient *http.Client, owner, repo string) (*g2.Identities, map[string]string, error) {
	ids, files, err := g2.ReadIdentities(ctx, logger, github.NewClient(httpClient).Branches(owner, repo))
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the error already says what it couldn't read
	}
	registry.UseIdentities(ids)
	return ids, files, nil
}
