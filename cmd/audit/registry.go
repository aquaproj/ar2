package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"go.yaml.in/yaml/v3"
)

// Widths the reports lay their columns out with.
const (
	exampleWidth = 40
	diffWidth    = 60
	detailWidth  = 90
)

// walkPackages calls fn for every package definition under root, which is the pkgs
// directory of a checked out aqua-registry.
//
// A file that doesn't parse is skipped rather than fatal: the point is to report on
// the ones that do, and aqua-registry's own CI is what says a definition is valid.
func walkPackages(root string, fn func(pkgInfo *aquaregistry.PackageInfo)) error {
	//nolint:gosec // root is a checkout of aqua-registry the developer running this points at
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "registry.yaml" {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // as above
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var cfg aquaregistry.Config
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil //nolint:nilerr // reported by aqua-registry's own CI
		}
		for _, pkgInfo := range cfg.PackageInfos {
			if pkgInfo != nil {
				fn(pkgInfo)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}
	return nil
}

// comparedFields are the fields whose values decide what aqua installs and how it
// verifies it. Everything else a definition holds is either the same in both by
// construction, such as the repository, or is read only to produce these.
//
// A difference in any of them is a difference a user would see; a difference
// anywhere else is a difference in how the definition is written.
func comparedFields(p *aquaregistry.PackageInfo) map[string]string {
	return map[string]string{
		"files":         render(p.Files),
		"replacements":  render(p.Replacements),
		"cosign":        render(p.Cosign),
		"slsa":          render(p.SLSAProvenance),
		"minisign":      render(p.Minisign),
		"attestations":  render(p.GitHubArtifactAttestations),
		"no_asset":      render(p.NoAsset),
		"error_message": render(p.ErrorMessage),
	}
}

// comparedFieldNames lists the fields in a fixed order, so that a report reads the
// same way twice.
func comparedFieldNames() []string {
	return []string{"files", "replacements", "cosign", "slsa", "minisign", "attestations", "no_asset", "error_message"}
}

// render turns a field into the text it is compared as. Marshalling rather than
// comparing the values themselves keeps a pointer and the value it points at from
// reading as different.
func render(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<unmarshalable: %v>", err)
	}
	return strings.TrimSpace(string(b))
}

// oneLine reduces a value to a single line so that a table stays a table.
func oneLine(s string, width int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > width {
		return s[:width] + "..."
	}
	return s
}
