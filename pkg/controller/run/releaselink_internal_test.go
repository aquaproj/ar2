package run

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

func config(typ, owner, name string) *aquag2.Config {
	return &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		Type: typ, RepoOwner: owner, RepoName: name,
	}}
}

// What a reader of a pull request wants next is the release, so a version is written as a link
// to it.
func TestReleasesOf_link(t *testing.T) {
	t.Parallel()
	rel := releasesOf(config("github_release", "ko-build", "ko"))
	want := "[v0.19.1](https://github.com/ko-build/ko/releases/tag/v0.19.1)"
	if got := rel.link("v0.19.1"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A version with a slash in it is a tag like any other.
	if got := rel.link("kustomize/v5.8.1"); got != "[kustomize/v5.8.1](https://github.com/ko-build/ko/releases/tag/kustomize/v5.8.1)" {
		t.Errorf("a version with a slash is written as %q", got)
	}
}

// A package whose versions aren't GitHub releases has no release page to link to: a crate's
// version isn't a tag, and a package whose definition hasn't arrived says nothing about where
// it comes from. Those are written plainly rather than linked to a page that isn't there.
func TestReleasesOf_nothingToLinkTo(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]*aquag2.Config{
		"a crate":        config("cargo", "sharkdp", "bat"),
		"no repository":  config("github_release", "", ""),
		"only a claim":   {PackageInfo: &aquaregistry.PackageInfo{Name: "cli/cli"}},
		"nothing at all": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := releasesOf(cfg).link("v1.0.0"); got != "v1.0.0" {
				t.Errorf("got %q, want the version itself", got)
			}
		})
	}
}
