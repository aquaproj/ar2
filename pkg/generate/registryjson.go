// Package generate builds aqua-registry-g2's registry.json from an upstream release.
package generate

import (
	"encoding/json"
	"fmt"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/g2"
)

// Registry is the content of versions/<version>/registry.json, and Asset is one of
// its entries.
//
// The types are aqua's. aqua reads what ar2 writes, so the format is described where
// it is read; defining it again here would be two descriptions of one thing kept in
// step by hand.
type (
	Registry = g2.Registry
	Asset    = g2.Asset
	File     = g2.File
)

// Marshal renders registry.json the way it is stored: indented two spaces, with a newline
// at the end.
//
// It is read in pull requests, where a file on one line makes every change a change to the
// whole file, and a person deciding whether to replace what is published has to read which
// field moved. What the indentation costs is little: over the 1,858 files the registry held
// it was 43% more bytes raw but 7.9% gzipped, about 40 bytes a file, and gzipped is how aqua
// downloads it and how git stores it.
//
// Every writer of registry.json goes through this, so a file generated again comes out the
// same bytes as the one it would replace.
func Marshal(reg *Registry) ([]byte, error) {
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal registry.json: %w", err)
	}
	return append(b, '\n'), nil
}

// StampRepoID records the definition's repository id on each entry that names that
// repository on GitHub.
//
// An entry names the repository it downloads from, which is nearly always the package's
// own. One that names another repository -- or a forge instance, or none at all -- gets no
// id: the definition's is not its.
func StampRepoID(reg *Registry, cfg *g2.Config) {
	if cfg == nil || cfg.PackageInfo == nil || cfg.RepoID == 0 {
		return
	}
	for _, asset := range reg.Assets {
		if asset.Host != "" || asset.RepoOwner == "" || asset.RepoName == "" {
			continue
		}
		if !strings.EqualFold(asset.RepoOwner, cfg.RepoOwner) || !strings.EqualFold(asset.RepoName, cfg.RepoName) {
			continue
		}
		asset.RepoID = cfg.RepoID
	}
}

// OwnSigning gives every entry a copy of its signing configuration of its own.
//
// An entry starts out with the definition's -- the same objects for every entry of every
// version a run generates -- and what is checked and recorded afterwards is about one
// entry: the attestation's certificate names the commit one version was built from, and
// cosign's command line is rendered for one asset. Written into a shared object, the first
// version's commit is what every later version is held to, and its verification fails.
func OwnSigning(reg *Registry) {
	for _, asset := range reg.Assets {
		asset.Cosign = copyCosign(asset.Cosign)
		asset.GitHubArtifactAttestations = copyAttestations(asset.GitHubArtifactAttestations)
		asset.Minisign = copyMinisign(asset.Minisign)
		asset.Packslip = copyPackslip(asset.Packslip)
		asset.SLSAProvenance = copySLSA(asset.SLSAProvenance)
	}
}

func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

func copyCosign(c *aquaregistry.Cosign) *aquaregistry.Cosign {
	if c == nil {
		return nil
	}
	out := *c
	out.Enabled = copyPtr(c.Enabled)
	if c.Opts != nil {
		out.Opts = append([]string{}, c.Opts...)
	}
	out.Signature = copyFile(c.Signature)
	out.Certificate = copyFile(c.Certificate)
	out.Key = copyFile(c.Key)
	out.Bundle = copyFile(c.Bundle)
	return &out
}

func copyFile(f *aquaregistry.DownloadedFile) *aquaregistry.DownloadedFile {
	if f == nil {
		return nil
	}
	out := *f
	out.Asset = copyPtr(f.Asset)
	out.URL = copyPtr(f.URL)
	return &out
}

func copyMinisign(m *aquaregistry.Minisign) *aquaregistry.Minisign {
	if m == nil {
		return nil
	}
	out := *m
	out.Enabled = copyPtr(m.Enabled)
	out.Asset = copyPtr(m.Asset)
	out.URL = copyPtr(m.URL)
	return &out
}

func copyPackslip(p *aquaregistry.Packslip) *aquaregistry.Packslip {
	if p == nil {
		return nil
	}
	out := *p
	out.Enabled = copyPtr(p.Enabled)
	out.URL = copyPtr(p.URL)
	return &out
}

func copySLSA(s *aquaregistry.SLSAProvenance) *aquaregistry.SLSAProvenance {
	if s == nil {
		return nil
	}
	out := *s
	out.Enabled = copyPtr(s.Enabled)
	out.Asset = copyPtr(s.Asset)
	out.URL = copyPtr(s.URL)
	out.SourceURI = copyPtr(s.SourceURI)
	return &out
}

func copyAttestations(a *aquaregistry.GitHubArtifactAttestations) *aquaregistry.GitHubArtifactAttestations {
	if a == nil {
		return nil
	}
	out := *a
	out.Enabled = copyPtr(a.Enabled)
	return &out
}
