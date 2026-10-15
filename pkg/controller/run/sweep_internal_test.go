package run

import (
	"testing"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/github"
	"github.com/aquaproj/ar2/pkg/state"
	"github.com/google/go-cmp/cmp"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	stale := now.Add(-deepCheckAge - time.Hour)
	swept := []string{"v2.0.0", "v1.0.0"}
	fp := state.Fingerprint(swept)

	tests := []struct {
		name  string
		pkg   *state.Package
		swept []string
		want  work
	}{
		{
			// The registry holds what upstream has, and the history was checked
			// recently. Nothing is asked about it at all.
			name:  "nothing to do",
			pkg:   &state.Package{Versions: fp, CaughtUp: true, LastDeepCheck: recent},
			swept: swept,
			want:  workNone,
		},
		{
			name:  "a new version appeared",
			pkg:   &state.Package{Versions: state.Fingerprint([]string{"v1.0.0"}), CaughtUp: true, LastDeepCheck: recent},
			swept: swept,
			want:  workSweep,
		},
		{
			// The versions are the ones last seen, but the registry didn't hold them
			// all: a pull request that hasn't merged, or work the budget cut short.
			name:  "the same versions, still not in the registry",
			pkg:   &state.Package{Versions: fp, CaughtUp: false, LastDeepCheck: recent},
			swept: swept,
			want:  workSweep,
		},
		{
			name:  "never backfilled",
			pkg:   &state.Package{Versions: fp, CaughtUp: true},
			swept: swept,
			want:  workDeep,
		},
		{
			// A release dated in the past doesn't appear among the newest, so the
			// history is walked again from time to time.
			name:  "the history is due a look",
			pkg:   &state.Package{Versions: fp, CaughtUp: true, LastDeepCheck: stale},
			swept: swept,
			want:  workDeep,
		},
		{
			// Gone, renamed, or the request was refused. Looking is the safe way
			// round: skipping would leave it skipped forever.
			name:  "the sweep found nothing",
			pkg:   &state.Package{Versions: fp, CaughtUp: true, LastDeepCheck: recent},
			swept: nil,
			want:  workDeep,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := decide(tt.pkg, tt.swept, now); got != tt.want {
				t.Errorf("decided %v, want %v", got, tt.want)
			}
		})
	}
}

// What the registry holds is the only thing that says a package is done with. A
// pull request that fails CI is never merged, and a package marked done for work
// that didn't land would never be looked at again.
func TestRecord(t *testing.T) {
	t.Parallel()
	versions := []string{"v2.0.0", "v1.0.0"}

	held := &state.Package{}
	record(held, nil, versions, map[string]struct{}{"v2.0.0": {}, "v1.0.0": {}}, workSweep)
	if !held.CaughtUp {
		t.Error("the registry held every version and the package isn't caught up")
	}
	if held.Versions != state.Fingerprint(versions) {
		t.Error("the versions weren't fingerprinted")
	}
	if !held.LastDeepCheck.IsZero() {
		t.Error("a sweep counted as having walked the history")
	}

	missing := &state.Package{}
	record(missing, nil, versions, map[string]struct{}{"v1.0.0": {}}, workDeep)
	if missing.CaughtUp {
		t.Error("a version the registry doesn't hold left the package caught up")
	}
	if missing.LastDeepCheck.IsZero() {
		t.Error("walking the history wasn't recorded")
	}
}

// What is fingerprinted is what the sweep found, which is what the next run compares it
// with, rather than what was left after the version_filter.
func TestRecord_swept(t *testing.T) {
	t.Parallel()
	swept := []string{"v2.0.0", "v2.0.0-nightly", "v1.0.0"}
	pkg := &state.Package{}
	record(pkg, swept, []string{"v2.0.0", "v1.0.0"}, map[string]struct{}{"v2.0.0": {}, "v1.0.0": {}}, workSweep)
	if pkg.Versions != state.Fingerprint(swept) {
		t.Error("the fingerprint isn't the sweep's")
	}
	if !pkg.CaughtUp {
		t.Error("the package isn't caught up")
	}
}

// A package with no release is read by its tags. One written as a prerelease is left out,
// unless the package asks for them or every tag is one.
func TestTaggedVersions(t *testing.T) {
	t.Parallel()
	tags := []string{"v4.3.0-rc2", "v4.2.0"}
	if diff := cmp.Diff([]string{"v4.2.0"}, taggedVersions(tags, false)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(tags, taggedVersions(tags, true)); diff != "" {
		t.Errorf("asked for prereleases (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"v0.1.0-alpha"}, taggedVersions([]string{"v0.1.0-alpha"}, false)); diff != "" {
		t.Errorf("nothing but prereleases (-want +got):\n%s", diff)
	}
}

// Packages versioned by their tags are asked for differently from those versioned
// by their releases, and a package whose versions needn't be releases is asked for both.
func TestSplitBySource(t *testing.T) {
	t.Parallel()
	candidates := []*Candidate{
		{Name: "cli/cli", Package: &state.Package{RepoOwner: "cli", RepoName: "cli"}},
		{Name: "flutter/flutter", Package: &state.Package{RepoOwner: "flutter", RepoName: "flutter"}},
		{Name: "apache/spark", Package: &state.Package{RepoOwner: "apache", RepoName: "spark"}},
		// Nothing to ask GitHub about.
		{Name: "no/repo", Package: &state.Package{}},
	}
	releases, tags, either := splitBySource(candidates, map[string]*aquaregistry.PackageInfo{
		"cli/cli":         {Type: aquaregistry.PkgInfoTypeGitHubRelease},
		"flutter/flutter": {Type: aquaregistry.PkgInfoTypeHTTP, VersionSource: versionSourceTag},
		"apache/spark":    {Type: aquaregistry.PkgInfoTypeHTTP},
	})
	if diff := cmp.Diff([]github.Repo{{Owner: "cli", Name: "cli"}}, releases); diff != "" {
		t.Errorf("the release packages are wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]github.Repo{{Owner: "flutter", Name: "flutter"}}, tags); diff != "" {
		t.Errorf("the tag packages are wrong (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]github.Repo{{Owner: "apache", Name: "spark"}}, either); diff != "" {
		t.Errorf("the packages read by releases or tags are wrong (-want +got):\n%s", diff)
	}
}

// One repository can hold several packages, so a rename of it is a rename of each of
// them, and what follows the repository in a package's name is kept.
func TestRename(t *testing.T) {
	t.Parallel()
	for _, d := range []struct {
		pkgName string
		repo    string
		to      string
		want    string
		ok      bool
	}{
		{
			pkgName: "sst/opencode", repo: "sst/opencode", to: "anomalyco/opencode",
			want: "anomalyco/opencode", ok: true,
		},
		{
			pkgName: "kubernetes/kubernetes/kubectl", repo: "kubernetes/kubernetes", to: "k8s/kubernetes",
			want: "k8s/kubernetes/kubectl", ok: true,
		},
		// A name that doesn't begin with its repository can't be rewritten this way,
		// and guessing would move the package to a name nobody chose.
		{pkgName: "something/else", repo: "sst/opencode", to: "anomalyco/opencode"},
	} {
		t.Run(d.pkgName, func(t *testing.T) {
			t.Parallel()
			got, ok := rename(d.pkgName, d.repo, d.to)
			if ok != d.ok {
				t.Fatalf("ok is %v, want %v", ok, d.ok)
			}
			if got != d.want {
				t.Errorf("got %q, want %q", got, d.want)
			}
		})
	}
}
