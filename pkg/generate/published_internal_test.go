package generate

import (
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

func provenance(asset string) *aquaregistry.SLSAProvenance {
	return &aquaregistry.SLSAProvenance{Type: "github_release", Asset: &asset}
}

// ko published multiple.intoto.jsonl up to v0.18.0 and not from v0.19.0, while the definition
// claims it for every version. The release is what says which, so the entry for the version
// that doesn't carry it doesn't claim it.
func TestDropUnpublished_provenance(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{Asset: "ko_0.19.0_Linux_x86_64.tar.gz", SLSAProvenance: provenance("multiple.intoto.jsonl")},
	}}
	dropUnpublished(discardLogger(), reg, names("ko_0.19.0_Linux_x86_64.tar.gz", "checksums.txt"))
	if reg.Assets[0].SLSAProvenance != nil {
		t.Error("the entry still claims a provenance the release doesn't carry")
	}
}

// The release that carries it keeps it, which is every version up to the one that stopped.
func TestDropUnpublished_provenanceThatIsThere(t *testing.T) {
	t.Parallel()
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{
		{Asset: "ko_0.18.0_Linux_x86_64.tar.gz", SLSAProvenance: provenance("multiple.intoto.jsonl")},
	}}
	dropUnpublished(discardLogger(), reg, names("ko_0.18.0_Linux_x86_64.tar.gz", "multiple.intoto.jsonl"))
	if reg.Assets[0].SLSAProvenance == nil {
		t.Error("the provenance the release carries was dropped")
	}
}

// A file in another repository's release is not something this asset list answers for. The
// same goes for one named by a URL, which carries no asset at all.
func TestDropUnpublished_anotherRepository(t *testing.T) {
	t.Parallel()
	asset := "multiple.intoto.jsonl"
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{{
		Asset: "tool.tar.gz",
		SLSAProvenance: &aquaregistry.SLSAProvenance{
			Type: "github_release", RepoOwner: "other", RepoName: "repo", Asset: &asset,
		},
	}, {
		Asset:    "tool.tar.gz",
		Minisign: &aquaregistry.Minisign{Type: "http"},
	}}}
	dropUnpublished(discardLogger(), reg, names("tool.tar.gz"))
	if reg.Assets[0].SLSAProvenance == nil {
		t.Error("a provenance in another repository's release was dropped")
	}
	if reg.Assets[1].Minisign == nil {
		t.Error("a signature named by a URL was dropped")
	}
}

// Cosign is verified as a whole, so a signature whose certificate isn't published is no more
// checkable than one that isn't published itself.
func TestDropUnpublished_cosign(t *testing.T) {
	t.Parallel()
	sig, cert := "tool.sig", "tool.pem"
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{{
		Asset: "tool.tar.gz",
		Cosign: &aquaregistry.Cosign{
			Signature:   &aquaregistry.DownloadedFile{Type: "github_release", Asset: &sig},
			Certificate: &aquaregistry.DownloadedFile{Type: "github_release", Asset: &cert},
		},
	}}}
	dropUnpublished(discardLogger(), reg, names("tool.tar.gz", "tool.sig"))
	if reg.Assets[0].Cosign != nil {
		t.Error("the entry still claims a Cosign configuration the release can't answer for")
	}
}

// A verification the entry has turned off claims nothing, so there is nothing to drop.
func TestDropUnpublished_disabled(t *testing.T) {
	t.Parallel()
	off := false
	asset := "multiple.intoto.jsonl"
	reg := &aquag2.Registry{Assets: []*aquag2.Asset{{
		Asset: "tool.tar.gz",
		SLSAProvenance: &aquaregistry.SLSAProvenance{
			Enabled: &off, Type: "github_release", Asset: &asset,
		},
	}}}
	dropUnpublished(discardLogger(), reg, names("tool.tar.gz"))
	if reg.Assets[0].SLSAProvenance == nil {
		t.Error("a verification that is turned off was dropped")
	}
}
