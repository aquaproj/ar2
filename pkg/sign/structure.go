package sign

import (
	"net/url"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

// Structure moves what cosign's command line says into the fields that say it, on an entry
// whose templates have been rendered.
//
// The files cosign is given -- the signature, the certificate, the key, the bundle -- go into
// the downloadable files aqua already has for them, and who the certificate must name goes
// into fields of its own. aqua turns both back into the same flags when it verifies, so the
// command is the same; what changes is that a reader can see what is verified without
// parsing a command line. A flag no field names stays in opts.
//
// A file is a release asset of the entry's own repository at this version when its URL says
// so, which is how it is downloaded with the same client as the asset; anything else is a
// URL.
func Structure(asset *generate.Asset, version string) {
	c := asset.Cosign
	if c == nil || len(c.Opts) == 0 {
		return
	}
	rest := []string{}
	for i := 0; i < len(c.Opts); {
		start := i
		flag, value, next, ok := flagValue(c.Opts, i)
		i = next
		if ok && structured(c, flag, value, asset, version) {
			continue
		}
		rest = append(rest, c.Opts[start:next]...)
	}
	if len(rest) == 0 {
		rest = nil
	}
	c.Opts = rest
}

// flagValue reads the flag at i and its value, written either as --flag=value or as two
// elements, and returns the index after them. It reports false for an element that isn't
// a flag with a value, which is then one element.
func flagValue(opts []string, i int) (string, string, int, bool) {
	opt := opts[i]
	if !strings.HasPrefix(opt, "--") {
		return "", "", i + 1, false
	}
	if flag, value, found := strings.Cut(opt, "="); found {
		return flag, value, i + 1, true
	}
	if i+1 >= len(opts) || strings.HasPrefix(opts[i+1], "--") {
		return "", "", i + 1, false
	}
	return opt, opts[i+1], i + 2, true //nolint:mnd // the flag and its value
}

// structured sets the field a flag names and reports whether there is one. A field already
// set is left alone and the flag kept, since the two would say different things.
func structured(c *aquaregistry.Cosign, flag, value string, asset *generate.Asset, version string) bool {
	text := map[string]*string{
		flagIdentity:           &c.CertificateIdentity,
		flagIdentityRegexp:     &c.CertificateIdentityRegexp,
		flagIssuer:             &c.CertificateOIDCIssuer,
		flagWorkflowRepository: &c.CertificateGitHubWorkflowRepository,
		flagWorkflowRef:        &c.CertificateGitHubWorkflowRef,
	}
	if field, ok := text[flag]; ok {
		if *field != "" {
			return false
		}
		*field = value
		return true
	}
	files := map[string]**aquaregistry.DownloadedFile{
		"--signature":   &c.Signature,
		"--certificate": &c.Certificate,
		"--key":         &c.Key,
		"--bundle":      &c.Bundle,
	}
	field, ok := files[flag]
	if !ok || *field != nil {
		return false
	}
	file := downloadedFile(value, asset, version)
	if file == nil {
		return false
	}
	*field = file
	return true
}

// downloadedFile is where a file cosign is given comes from: a release asset of the entry's
// repository at this version, or a URL. A value that is neither -- a path on the machine,
// say -- isn't something aqua could download, and has no field.
func downloadedFile(value string, asset *generate.Asset, version string) *aquaregistry.DownloadedFile {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	if name, ok := releaseAsset(u, asset, version); ok {
		return &aquaregistry.DownloadedFile{
			Type:      "github_release",
			RepoOwner: asset.RepoOwner,
			RepoName:  asset.RepoName,
			Asset:     &name,
		}
	}
	return &aquaregistry.DownloadedFile{Type: "http", URL: &value}
}

// releaseAsset reads the asset name out of a URL of a release asset of the entry's own
// repository at this version, and reports false for any other URL.
func releaseAsset(u *url.URL, asset *generate.Asset, version string) (string, bool) {
	if asset.Host != "" || asset.RepoOwner == "" || asset.RepoName == "" {
		return "", false
	}
	owner, repo, tag, name, ok := parseReleaseURL(u)
	if !ok || tag != version {
		return "", false
	}
	if !strings.EqualFold(owner, asset.RepoOwner) || !strings.EqualFold(repo, asset.RepoName) {
		return "", false
	}
	return name, true
}

// releaseURLParts is how many segments the path of a release asset's URL has:
// owner/repo/releases/download/tag/asset.
const releaseURLParts = 6

// parseReleaseURL splits https://github.com/owner/repo/releases/download/tag/asset into its
// parts, unescaped, and reports false for any other URL.
func parseReleaseURL(u *url.URL) (string, string, string, string, bool) {
	if u.Host != "github.com" {
		return "", "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != releaseURLParts || parts[2] != "releases" || parts[3] != "download" {
		return "", "", "", "", false
	}
	tag, err := url.PathUnescape(parts[4])
	if err != nil {
		return "", "", "", "", false
	}
	name, err := url.PathUnescape(parts[5])
	if err != nil || name == "" {
		return "", "", "", "", false
	}
	return parts[0], parts[1], tag, name, true
}
