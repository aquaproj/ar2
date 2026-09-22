package run

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/szksh-lab-2/ar2/pkg/generate"
)

// signingKinds names each way an asset can be verified, in the order they are
// reported. The names are the fields they are written as, so a warning and the file
// it is about say the same thing.
var signingKinds = []struct { //nolint:gochecknoglobals
	name string
	has  func(*generate.Asset) bool
}{
	{"cosign", func(a *generate.Asset) bool { return a.Cosign.GetEnabled() }},
	{"github_artifact_attestations", func(a *generate.Asset) bool { return a.GitHubArtifactAttestations.GetEnabled() }},
	{"minisign", func(a *generate.Asset) bool { return a.Minisign.GetEnabled() }},
	{"slsa_provenance", func(a *generate.Asset) bool { return a.SLSAProvenance.GetEnabled() }},
}

// assetKey identifies the entry a version's asset is compared against: the same
// environment in another version.
//
// Variants are part of it because a package can have more than one build for an
// os and arch, and a musl build losing its signature is not something a glibc one
// still having its own makes up for.
func assetKey(a *generate.Asset) string {
	if len(a.Variants) == 0 {
		return a.OS + "/" + a.Arch
	}
	keys := make([]string, 0, len(a.Variants))
	for k := range a.Variants {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(a.OS + "/" + a.Arch)
	for _, k := range keys {
		b.WriteString("/" + k + "=" + a.Variants[k])
	}
	return b.String()
}

// lostSigning returns what the newer version can no longer be verified with,
// as "<environment>: <kind>" for each one.
//
// A release that stops carrying its signatures is what an attacker publishing one
// looks like from here: the assets are still named the same and still hash to
// something, and nothing else about the generated file would look wrong. It is also
// what a mistake in converting a definition looks like — the boundary bug that
// silently cost tfcmt v4.14.0 its attestations would have shown up here.
//
// Gaining a signature, or an environment the older version didn't have, is not a
// loss. Neither is a signature being configured differently: what matters is whether
// the asset can still be verified at all.
func lostSigning(older, newer *aquag2.Registry) []string {
	before := make(map[string]*generate.Asset, len(older.Assets))
	for _, a := range older.Assets {
		before[assetKey(a)] = a
	}

	var lost []string
	for _, a := range newer.Assets {
		old, ok := before[assetKey(a)]
		if !ok {
			continue
		}
		for _, kind := range signingKinds {
			if kind.has(old) && !kind.has(a) {
				lost = append(lost, assetKey(a)+": "+kind.name)
			}
		}
	}
	return lost
}

// checkSigning marks the versions that can be verified with less than the one
// before them, so that they are left for review instead of merging themselves.
//
// The versions come newest first, and baseline is the newest version the repository
// already holds. Without one — a package nothing has been generated for yet — the
// oldest version in this run stands in, which is enough to catch a newest release
// that dropped what the ones before it had.
func checkSigning(logger *slog.Logger, pkgName string, versions []*version, baseline *aquag2.Registry) {
	if baseline == nil {
		if len(versions) < 2 { //nolint:mnd // one version and nothing to compare it against
			return
		}
		baseline = versions[len(versions)-1].Registry
		versions = versions[:len(versions)-1]
	}
	for _, v := range versions {
		lost := lostSigning(baseline, v.Registry)
		if len(lost) == 0 {
			continue
		}
		logger.Warn("this version can be verified with less than the one before it",
			"package", pkgName, "version", v.Version, "lost", strings.Join(lost, ", "))
		v.NeedsReview = true
		v.LostSigning = lost
	}
}

// baselineVersion returns the newest version the repository already holds.
//
// versions is the release list, newest first, so the first one the repository has is
// the newest. Sorting isn't needed and neither is a request: the list was fetched to
// decide what to generate.
func baselineVersion(versions []string, existing map[string]struct{}) string {
	for _, tag := range versions {
		if _, ok := existing[tag]; ok {
			return tag
		}
	}
	return ""
}

// signingBaseline reads the registry.json this run's versions are compared against,
// and reports whether the comparison can be made at all.
//
// A version that couldn't be read is not the same as a package that has none. The
// check exists to catch a release that quietly stopped being verifiable, so when it
// can't run, the versions go out for review rather than merging unexamined.
func (c *Controller) signingBaseline(ctx context.Context, logger *slog.Logger, pkgName, version string) (*aquag2.Registry, bool) {
	if version == "" {
		return nil, true
	}
	reg, err := c.g2.Version(ctx, pkgName, version)
	if err != nil {
		logger.Warn("failed to read the version to compare the signing against",
			"package", pkgName, "version", version, "error", err.Error())
		return nil, false
	}
	return reg, true
}
