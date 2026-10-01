package generate

import (
	"log/slog"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// dropUnpublished takes out of each entry the verifications whose file the release doesn't
// carry.
//
// A definition says how a package's assets are verified for every version of it, and an
// upstream can stop publishing the file: ko published multiple.intoto.jsonl up to v0.18.0 and
// not from v0.19.0. An entry claiming it anyway isn't true of that version, and what says so
// is the check that downloads it -- which is the check, so the version never merges and the
// package stops moving.
//
// Whether the file is in the release is in the asset list, so it belongs with what the
// generation reads rather than with what a definition has to say per version. This is also
// why it runs before the signing is inferred: a release that moved from one way of signing to
// another has the old one dropped here and the new one read off the same list.
//
// Only a file the entry says is in this release is ruled on. One named in another repository's
// release, or by a URL, is not something this asset list answers for, and one the entry has
// turned off isn't a claim at all.
//
// The checksum is not among them. ar2 fills it from the digest the release publishes or by
// hashing the asset, so what an entry says about a checksum isn't a claim that a file is
// there.
func dropUnpublished(logger *slog.Logger, reg *Registry, assetNames map[string]struct{}) {
	for _, asset := range reg.Assets {
		if p := asset.SLSAProvenance; p != nil &&
			!published(assetNames, enabled(p.Enabled), p.Type, p.RepoOwner, p.RepoName, p.Asset) {
			logger.Warn("the release doesn't carry the provenance the definition names",
				"asset", asset.Asset, "provenance", *p.Asset)
			asset.SLSAProvenance = nil
		}
		if m := asset.Minisign; m != nil &&
			!published(assetNames, enabled(m.Enabled), m.Type, m.RepoOwner, m.RepoName, m.Asset) {
			logger.Warn("the release doesn't carry the minisign signature the definition names",
				"asset", asset.Asset, "signature", *m.Asset)
			asset.Minisign = nil
		}
		if c := asset.Cosign; c != nil && !cosignPublished(assetNames, c) {
			logger.Warn("the release doesn't carry the file the definition's Cosign configuration names",
				"asset", asset.Asset)
			asset.Cosign = nil
		}
	}
}

// cosignPublished reports whether the release carries every file the Cosign configuration
// names.
//
// Every one of them, because what is verified is the whole: a signature without the
// certificate it was made with is not something cosign can check.
func cosignPublished(assetNames map[string]struct{}, cosign *aquaregistry.Cosign) bool {
	on := enabled(cosign.Enabled)
	for _, file := range []*aquaregistry.DownloadedFile{
		cosign.Signature, cosign.Certificate, cosign.Key, cosign.Bundle,
	} {
		if file == nil {
			continue
		}
		if !published(assetNames, on, file.Type, file.RepoOwner, file.RepoName, file.Asset) {
			return false
		}
	}
	return true
}

// published reports whether the release carries the file a verification names.
//
// Anything this can't rule on is published as far as it is concerned: the question is
// whether a file the entry says is in this release is in the asset list, and nothing else.
func published(assetNames map[string]struct{}, on bool, typ, repoOwner, repoName string, asset *string) bool {
	if !on || typ != aquaregistry.PkgInfoTypeGitHubRelease {
		return true
	}
	if asset == nil || *asset == "" {
		return true
	}
	if repoOwner != "" || repoName != "" {
		// Another repository's release, which this asset list says nothing about.
		return true
	}
	_, ok := assetNames[*asset]
	return ok
}

// enabled reads a verification's switch, which is on unless it says otherwise.
func enabled(b *bool) bool {
	return b == nil || *b
}
