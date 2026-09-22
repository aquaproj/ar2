// Package attest checks that a release carries the GitHub artifact attestations its
// definition says it does.
//
// Nothing about an attestation is visible in a release's asset list: it is held by
// GitHub, against the artifact's digest. A definition claiming an attestation that
// isn't there would produce a registry.json aqua can't install from, and — worse —
// a release that stopped being attested would otherwise look like any other.
package attest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
	gogithub "github.com/google/go-github/v92/github"
)

// Attestations is the part of GitHub's API that answers whether an artifact is
// attested.
type Attestations interface {
	ListAttestations(ctx context.Context, owner, repo, subjectDigest string, opts *gogithub.ListOptions) (*gogithub.AttestationsResponse, *gogithub.Response, error)
}

// Checker asks GitHub whether an artifact is attested.
type Checker struct {
	gh Attestations
}

// New creates a Checker.
func New(gh Attestations) *Checker {
	return &Checker{gh: gh}
}

// Check drops the attestation from every asset that doesn't have one, and reports
// whether anything was dropped.
//
// Only the assets whose definition claims an attestation are looked up, which is
// what keeps the cost off the packages that don't use them: a lookup is a request
// per asset, and a run generates far more assets than it could afford that for.
// A package gaining attestations is therefore not noticed here; whoever reviews its
// definition adds them, the same way it works in aqua-registry today.
//
// A dropped attestation is a change the caller has to act on. It leaves the
// generated file honest — it says what can actually be verified — and the comparison
// against the version before it is what turns that into a pull request nobody merges
// by accident.
func (c *Checker) Check(ctx context.Context, logger *slog.Logger, pkgName string, reg *generate.Registry) (bool, error) {
	owner, name, err := repo(pkgName)
	if err != nil {
		return false, err
	}
	dropped := false
	for _, asset := range reg.Assets {
		if !asset.GitHubArtifactAttestations.GetEnabled() || asset.Checksum == "" {
			continue
		}
		attested, err := c.attested(ctx, owner, name, asset.Checksum)
		if err != nil {
			return false, fmt.Errorf("ask whether %s/%s is attested: %w", asset.OS, asset.Arch, err)
		}
		if attested {
			continue
		}
		logger.Warn("the definition claims an attestation the release doesn't have",
			"package", pkgName, "os", asset.OS, "arch", asset.Arch)
		asset.GitHubArtifactAttestations = nil
		dropped = true
	}
	return dropped, nil
}

// attested reports whether GitHub holds an attestation for the digest.
func (c *Checker) attested(ctx context.Context, owner, name, digest string) (bool, error) {
	res, resp, err := c.gh.ListAttestations(ctx, owner, name, "sha256:"+digest,
		&gogithub.ListOptions{PerPage: 1})
	if err != nil {
		// An artifact that isn't attested comes back as an empty list, which is
		// not an error. A repository that has never attested anything can answer
		// 404 instead, which means the same thing.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("list the attestations: %w", err)
	}
	return len(res.Attestations) > 0, nil
}

// repo returns the repository a package is released from. A package name can have
// more than two segments — a monorepo publishing several binaries — and the
// repository is the first two.
func repo(pkgName string) (string, string, error) {
	owner, name, found := strings.Cut(pkgName, "/")
	if !found {
		return "", "", fmt.Errorf("%w: %s", errPkgName, pkgName)
	}
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	return owner, name, nil
}
