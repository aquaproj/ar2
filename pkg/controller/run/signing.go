package run

import (
	"context"
	"log/slog"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode"

	aquaasset "github.com/aquaproj/aqua/v2/pkg/asset"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/generate"
	goversion "github.com/hashicorp/go-version"
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
	{"packslip", func(a *generate.Asset) bool { return a.Packslip.GetEnabled() }},
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

// lostEnvironments returns the environments the newer version doesn't cover and the
// older one did.
//
// Which is what an asset the release renamed looks like. The names of the assets are
// read from the release rather than from a template, so a spelling the parser doesn't
// know -- luau-lang/luau calling its Linux build luau-ubuntu.zip -- produces no entry
// for that environment at all rather than a wrong one. Nothing else about the generated
// file would look wrong: what it says is true, it just stops saying anything about
// Linux, and whoever installs the package there is told the package doesn't support it.
//
// The definition is where the answer goes, as a replacement naming the spelling, and
// that is a person's job: a spelling nobody has seen before can't be derived from the
// release that uses it.
//
// Gaining an environment is not a loss, and neither is a package that never had one.
//
// An entry that named no variant was the platform's, and an entry of the platform for any
// variant still covers it. astral-sh/uv's 0.13.0 has one entry for linux/amd64, the musl
// build, generated before the libc variants were read off the names; 0.13.1 has one for
// glibc and one for musl, and read entry by entry it had lost linux/amd64.
func lostEnvironments(older, newer *aquag2.Registry) []string {
	now := map[string]struct{}{}
	for _, a := range newer.Assets {
		now[assetKey(a)] = struct{}{}
		now[a.OS+"/"+a.Arch] = struct{}{}
	}
	var lost []string
	for _, a := range older.Assets {
		if _, ok := now[assetKey(a)]; !ok {
			lost = append(lost, assetKey(a))
		}
	}
	sort.Strings(lost)
	return lost
}

// gainedEnvironments returns the platforms, as os/arch, the newer version covers and the
// older one didn't.
//
// Gaining one is not a loss, but it is a change somebody should see before it merges: the
// asset names are read from the release, and an asset that is placed in an environment it
// wasn't in before is as likely to be another program the release started publishing as a
// build of the package. A libc variant of a platform the older version already covered is
// not a new platform.
func gainedEnvironments(older, newer *aquag2.Registry) []string {
	before := make(map[string]struct{}, len(older.Assets))
	for _, a := range older.Assets {
		before[a.OS+"/"+a.Arch] = struct{}{}
	}
	seen := map[string]struct{}{}
	var gained []string
	for _, a := range newer.Assets {
		env := a.OS + "/" + a.Arch
		if _, ok := before[env]; ok {
			continue
		}
		if _, ok := seen[env]; ok {
			continue
		}
		seen[env] = struct{}{}
		gained = append(gained, env)
	}
	sort.Strings(gained)
	return gained
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
		compareWith(logger, pkgName, v, baseline)
	}
}

// compareWith marks the version for review when it can be verified with less than the
// version before it, covers fewer environments, or covers a platform that one didn't with
// an asset named unlike the rest. A platform gained with an asset named like the rest is
// recorded, to be said in the pull request, and merges.
func compareWith(logger *slog.Logger, pkgName string, v *version, before *aquag2.Registry) {
	if lost := lostSigning(before, v.Registry); len(lost) > 0 {
		logger.Warn("this version can be verified with less than the one before it",
			"package", pkgName, "version", v.Version, "lost", strings.Join(lost, ", "))
		v.NeedsReview = true
		v.LostSigning = lost
	}
	if lost := lostEnvironments(before, v.Registry); len(lost) > 0 {
		logger.Warn("this version covers fewer environments than the one before it",
			"package", pkgName, "version", v.Version, "lost", strings.Join(lost, ", "))
		v.NeedsReview = true
		v.LostEnvironments = lost
	}
	if gained := gainedEnvironments(before, v.Registry); len(gained) > 0 {
		logger.Info("this version covers platforms the one before it didn't",
			"package", pkgName, "version", v.Version, "gained", strings.Join(gained, ", "))
		v.GainedEnvironments = gained
		if strangers := strangerEnvironments(v.Registry, v.Version, gained); len(strangers) > 0 {
			logger.Warn("a platform this version gained is given an asset named unlike the others",
				"package", pkgName, "version", v.Version, "platforms", strings.Join(strangers, ", "))
			v.NeedsReview = true
			v.StrangerEnvironments = strangers
		}
	}
}

// strangerEnvironments returns the gained platforms whose asset is named unlike every
// asset of the platforms the version already covered.
//
// A platform a version gains is almost always a build the release started publishing,
// and that merges as it is: of 206 pull requests left for review for nothing else, not
// one had a wrong entry for the platform it gained. What the review was there for is the
// other reason a platform can come: the asset names are read from the release, and
// another program of the same release is placed wherever its name says.
// DeNA/unity-meta-check's gh-action-yaml-gen-darwin-amd64 was given to gh-action, whose
// builds are gh-action-linux-amd64, and greymd/teip's Windows installer,
// teip_installer-<version>-x86_64-pc-windows-msvc.exe, to teip.
//
// Another program is told apart by its name: what comes before the os, arch and version
// is the program's name, and it differs. That is what is compared, so a platform whose
// asset is named like the rest merges itself and one that isn't is left for review.
func strangerEnvironments(reg *aquag2.Registry, version string, gained []string) []string {
	isGained := make(map[string]struct{}, len(gained))
	for _, env := range gained {
		isGained[env] = struct{}{}
	}
	known := map[string]struct{}{}
	for _, a := range reg.Assets {
		if _, ok := isGained[a.OS+"/"+a.Arch]; ok {
			continue
		}
		if stem := assetStem(a, version); stem != "" {
			known[stem] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	var strangers []string
	for _, a := range reg.Assets {
		env := a.OS + "/" + a.Arch
		if _, ok := isGained[env]; !ok {
			continue
		}
		if _, ok := seen[env]; ok {
			continue
		}
		if _, ok := known[assetStem(a, version)]; ok {
			continue
		}
		seen[env] = struct{}{}
		strangers = append(strangers, env)
	}
	sort.Strings(strangers)
	return strangers
}

// assetStem is the part of an asset's name before the os, arch, version or format: the
// name of the program, as far as the name says. It is lowercased and has the separator
// before the first of them taken off, so dysk_3.8.2.zip and dysk-v3.8.2-x86_64-pc-windows-gnu.zip
// are both dysk. A name with none of them is all stem: alexellis/arkade's Linux build is
// arkade. It is "" for an entry that names no asset.
func assetStem(a *generate.Asset, version string) string {
	name := a.Asset
	if name == "" && a.URL != "" {
		name = path.Base(a.URL)
	}
	if name == "" {
		return ""
	}
	tmpl := aquaasset.ParseAssetName(name, version).Template
	if i := strings.Index(tmpl, "{{"); i >= 0 {
		tmpl = tmpl[:i]
	}
	return strings.ToLower(strings.TrimRight(tmpl, "-_."))
}

// before is the version a generated version is compared with: the nearest older one the
// registry holds or this run generated, or "" when there is none.
//
// The newest the registry holds is that for a new release, which is what the comparison
// is for: a release that renamed an asset or stopped signing. A version older than it is
// one a run fills the history in with, and compared with the newest it had lost what
// the package gained later: volta-cli/volta's 1.0.8 had no linux/arm64 build, which came
// after it. Compared with the version before it, it lost nothing. Versions this run
// generated count, so that a history filled in newest first is compared step by step.
//
// Older is by version number where the tags are versions, and by the order the releases
// came in otherwise. The order of the releases is when they were published, and a project
// that maintains several lines releases them interleaved: coder/coder published v2.29.30
// right after v2.34.22, so v2.29.30 was compared with v2.34.22 and reported as having lost
// the build provenance that line has and the 2.29 line never had. The version before
// v2.29.30 is v2.29.29.
func before(tag string, versions []string, existing map[string]struct{}, generated map[string]*version) string {
	known := func(v string) bool {
		_, held := existing[v]
		_, made := generated[v]
		return held || made
	}
	if older, ok := beforeByNumber(tag, versions, known); ok {
		return older
	}
	i := slices.Index(versions, tag)
	if i < 0 {
		return ""
	}
	for _, older := range versions[i+1:] {
		if known(older) {
			return older
		}
	}
	return ""
}

// beforeByNumber is the highest version below tag among those known, comparing only the
// tags that read as versions with the same prefix -- v for v1.2.3, faber-v for
// faber-v1.6.0. It says false when tag doesn't read as a version, and the order of the
// releases decides instead.
func beforeByNumber(tag string, versions []string, known func(string) bool) (string, bool) {
	prefix, number := splitVersion(tag)
	if number == nil {
		return "", false
	}
	best := ""
	var bestNumber *goversion.Version
	for _, v := range versions {
		if v == tag || !known(v) {
			continue
		}
		p, n := splitVersion(v)
		if n == nil || p != prefix || !n.LessThan(number) {
			continue
		}
		if bestNumber == nil || n.GreaterThan(bestNumber) {
			best, bestNumber = v, n
		}
	}
	return best, true
}

// splitVersion splits a tag into what comes before its first digit and the version that
// follows, or returns a nil version for a tag the rest of which isn't one.
func splitVersion(tag string) (string, *goversion.Version) {
	i := strings.IndexFunc(tag, unicode.IsDigit)
	if i < 0 {
		return "", nil
	}
	n, err := goversion.NewSemver(tag[i:])
	if err != nil {
		return "", nil
	}
	return tag[:i], n
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
