package migrate

import (
	"slices"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// assetsFilterFromTemplates says which of a release's assets are the package's, from the
// asset names aqua-registry gave it.
//
// The conversion leaves the asset name to the release, which is right for a repository
// that publishes one command. A repository publishing several -- sqldef/sqldef builds
// mysqldef, psqldef and sqlite3def, pgrok/pgrok builds pgrok and pgrokd -- has each of
// them as a package of its own, and the asset name was the only thing that told them
// apart. Without it the inference takes whichever command it finds first, and the
// package's files are looked for in another command's archive.
//
// Such a package is named after the command inside the repository rather than after the
// repository, so that is when the filter is written. Each template becomes a pattern by
// letting every {{...}} stand for anything, and an asset is the package's when it matches
// any of them. A signature or checksum named after an asset matches too, which is what
// the inference reads signing from.
//
// A template with a character this doesn't know how to write into an expression gives no
// filter at all, rather than one that matches something else.
func assetsFilterFromTemplates(pkgName string, pkgInfo *aquaregistry.PackageInfo) string {
	if pkgInfo.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return ""
	}
	if pkgName == pkgInfo.RepoOwner+"/"+pkgInfo.RepoName {
		return ""
	}
	var patterns []string
	for _, asset := range assetTemplates(pkgInfo) {
		pattern, ok := templatePattern(asset)
		if !ok {
			return ""
		}
		if !slices.Contains(patterns, pattern) {
			patterns = append(patterns, pattern)
		}
	}
	if len(patterns) == 0 {
		return ""
	}
	conds := make([]string, len(patterns))
	for i, p := range patterns {
		conds[i] = `Asset matches "` + p + `"`
	}
	return strings.Join(conds, " or ")
}

// assetTemplates returns every asset name the definition gives, in the order it gives
// them: the package's, its overrides', and each version's.
func assetTemplates(pkgInfo *aquaregistry.PackageInfo) []string {
	var out []string
	add := func(asset string, overrides []*aquaregistry.Override) {
		if asset != "" {
			out = append(out, asset)
		}
		for _, ov := range overrides {
			if ov.Asset != "" {
				out = append(out, ov.Asset)
			}
		}
	}
	add(pkgInfo.Asset, pkgInfo.Overrides)
	for _, vo := range pkgInfo.VersionOverrides {
		add(vo.Asset, vo.Overrides)
	}
	return out
}

// templatePattern turns an asset template into an anchored regular expression in which
// every {{...}} stands for one or more characters.
//
// The characters a regular expression gives a meaning to are written as a class of one,
// [.], so that nothing needs a backslash inside the expression's string literal.
func templatePattern(template string) (string, bool) {
	var b strings.Builder
	b.WriteString("^")
	rest := template
	for rest != "" {
		start := strings.Index(rest, "{{")
		literal := rest
		if start >= 0 {
			literal = rest[:start]
		}
		for _, c := range literal {
			switch c {
			case '\\', '"', '^', ']', '[', '$':
				return "", false
			case '.', '+', '*', '?', '(', ')', '|', '{', '}':
				b.WriteString("[" + string(c) + "]")
			default:
				b.WriteRune(c)
			}
		}
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return "", false
		}
		b.WriteString(".+")
		rest = rest[start+end+2:]
	}
	b.WriteString("$")
	return b.String(), true
}
