package run

import (
	"fmt"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// releases is where a package's versions are published, for linking to the one a version
// came from.
//
// What a reader of a pull request wants next is the release: whether the upstream said
// anything about the assets it renamed, or the signature it stopped publishing, is in its
// notes and nowhere in the generated file.
//
// A link is only written for a package whose versions are GitHub releases, because then the
// version is the tag and the page is the release. A crate's version isn't a tag -- bat
// publishes 0.25.0 to crates.io and tags v0.25.0 -- and a package whose definition hasn't
// arrived yet says nothing about where it comes from, so those are written plainly rather
// than linked to a page that isn't there.
type releases struct {
	owner string
	name  string
}

// releasesOf reads where a package's versions are published out of its definition.
func releasesOf(cfg *aquag2.Config) releases {
	if cfg == nil || cfg.PackageInfo == nil || cfg.Type != aquaregistry.PkgInfoTypeGitHubRelease {
		return releases{}
	}
	return releases{owner: cfg.RepoOwner, name: cfg.RepoName}
}

// link renders a version as a link to the release it came from, or as itself.
func (r releases) link(version string) string {
	if r.owner == "" || r.name == "" {
		return version
	}
	return fmt.Sprintf("[%s](https://github.com/%s/%s/releases/tag/%s)", version, r.owner, r.name, version)
}
