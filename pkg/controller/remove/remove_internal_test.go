package remove

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

const registryConfig = `---
ignored_packages:
  - name: scenarigo/scenarigo
    reason: |
      Its asset name carries the Go version it was built against.
`

// fakeRegistry stands in for aqua-registry-g2 and records what was written to it.
type fakeRegistry struct {
	inFlight     map[string]struct{}
	index        *aquag2.Index
	versionFiles []string
	// commits is what was committed, by branch.
	commits map[string][]*g2.File
	// bases is what each pull request was opened into, by head branch.
	bases  map[string]string
	titles []string
	bodies []string
}

func (f *fakeRegistry) PackagesInFlight(_ context.Context) (map[string]struct{}, error) {
	return f.inFlight, nil
}

func (f *fakeRegistry) File(_ context.Context, _, path string) (string, error) {
	if path == g2.RegistryConfigFileName {
		return registryConfig, nil
	}
	return "", nil
}

func (f *fakeRegistry) Index(_ context.Context, _ string) (*aquag2.Index, error) {
	if f.index == nil {
		return &aquag2.Index{}, nil
	}
	return f.index, nil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, branch string) (string, error) {
	return branch + "-sha", nil
}

func (f *fakeRegistry) VersionFiles(_ context.Context, _ string) ([]string, error) {
	return f.versionFiles, nil
}

func (f *fakeRegistry) Commit(_ context.Context, branch, _, _ string, files []*g2.File) error {
	if f.commits == nil {
		f.commits = map[string][]*g2.File{}
	}
	f.commits[branch] = files
	return nil
}

func (f *fakeRegistry) CreatePullRequestFrom(_ context.Context, head, base, title, body string) (*gogithub.PullRequest, error) {
	if f.bases == nil {
		f.bases = map[string]string{}
	}
	f.bases[head] = base
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	return &gogithub.PullRequest{Number: new(len(f.bases))}, nil
}

func logger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func index() *aquag2.Index {
	return &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli"},
		{Name: "foo/bar", Aliases: []string{"foo/old"}},
	}}
}

// Removing a package is three things at once: it stops being generated, stops being
// listed, and stops being held. The first two are one pull request into the default
// branch and the third is one into the package's branch.
func TestController_Remove(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		inFlight:     map[string]struct{}{},
		index:        index(),
		versionFiles: []string{"versions/v1.0.0/registry-1.json", "versions/v1.1.0/registry-1.json"},
	}
	if err := New(reg, "main").Remove(t.Context(), logger(), &Input{
		PkgName: "foo/bar",
		Reason:  "It is malware.",
	}); err != nil {
		t.Fatal(err)
	}

	// The default branch's half.
	stop := reg.commits[g2.RemoveBranchName("foo/bar")]
	if len(stop) != 3 {
		t.Fatalf("committed %d files to the removal branch", len(stop))
	}
	byPath := map[string]string{}
	for _, file := range stop {
		byPath[file.Path] = file.Content
	}
	if !strings.Contains(byPath[g2.RegistryConfigFileName], "- name: foo/bar") {
		t.Errorf("the package isn't ignored:\n%s", byPath[g2.RegistryConfigFileName])
	}
	if strings.Contains(byPath[aquag2.IndexFileName], "foo/bar") {
		t.Errorf("the catalogue still lists it:\n%s", byPath[aquag2.IndexFileName])
	}
	// The alias went with the entry, or a name nothing holds would still resolve.
	if strings.Contains(byPath[aquag2.AliasesFileName], "foo/old") {
		t.Errorf("the alias is still there:\n%s", byPath[aquag2.AliasesFileName])
	}
	if reg.bases[g2.RemoveBranchName("foo/bar")] != "main" {
		t.Errorf("the first pull request goes into %q", reg.bases[g2.RemoveBranchName("foo/bar")])
	}
}

// The package branch's half: every file it holds under versions/ goes, as deletions, in a
// pull request that says the other one merges first.
func TestController_Remove_dropsWhatWasGenerated(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		inFlight:     map[string]struct{}{},
		index:        index(),
		versionFiles: []string{"versions/v1.0.0/registry-1.json", "versions/v1.1.0/registry-1.json"},
	}
	if err := New(reg, "main").Remove(t.Context(), logger(), &Input{
		PkgName: "foo/bar",
		Reason:  "It is malware.",
	}); err != nil {
		t.Fatal(err)
	}

	drop := reg.commits[g2.HeadBranchName("foo/bar")]
	if len(drop) != 2 {
		t.Fatalf("committed %d files to the head branch", len(drop))
	}
	for _, file := range drop {
		if !file.Deleted {
			t.Errorf("%s isn't a deletion", file.Path)
		}
	}
	if reg.bases[g2.HeadBranchName("foo/bar")] != g2.BranchName("foo/bar") {
		t.Errorf("the second pull request goes into %q", reg.bases[g2.HeadBranchName("foo/bar")])
	}
	// It has to say which one merges first, because merging them the other way round
	// has the next run generate the files again.
	if !strings.Contains(reg.bodies[1], "#1") {
		t.Errorf("the second pull request doesn't point at the first:\n%s", reg.bodies[1])
	}
}

// A package that never had a version published is ignored and delisted, and there is
// nothing to take off its branch.
func TestController_Remove_nothingPublished(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{inFlight: map[string]struct{}{}, index: index()}
	if err := New(reg, "main").Remove(t.Context(), logger(), &Input{
		PkgName: "foo/bar",
		Reason:  "It is malware.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.commits[g2.HeadBranchName("foo/bar")]; ok {
		t.Error("it committed to the package branch")
	}
	if len(reg.bases) != 1 {
		t.Errorf("opened %d pull requests", len(reg.bases))
	}
}

// A package already ignored still has its entry and its files taken away: this is a
// removal being finished rather than started, and the list must not say it twice.
func TestController_Remove_alreadyIgnored(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		inFlight:     map[string]struct{}{},
		index:        index(),
		versionFiles: []string{"versions/v1.0.0/registry-1.json"},
	}
	if err := New(reg, "main").Remove(t.Context(), logger(), &Input{
		PkgName: "scenarigo/scenarigo",
		Reason:  "It is malware.",
	}); err != nil {
		t.Fatal(err)
	}
	for _, file := range reg.commits[g2.RemoveBranchName("scenarigo/scenarigo")] {
		if file.Path == g2.RegistryConfigFileName {
			t.Errorf("the configuration was written again:\n%s", file.Content)
		}
	}
	if len(reg.commits[g2.HeadBranchName("scenarigo/scenarigo")]) != 1 {
		t.Error("the generated file wasn't taken away")
	}
}

// A package with a pull request open is refused: its head branch is where the versions
// are taken out, so going ahead would discard what that pull request holds.
func TestController_Remove_pullRequestInFlight(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{inFlight: map[string]struct{}{g2.HeadBranchName("foo/bar"): {}}, index: index()}
	err := New(reg, "main").Remove(t.Context(), logger(), &Input{PkgName: "foo/bar", Reason: "It is malware."})
	if !errors.Is(err, errPullRequestInFlight) {
		t.Fatalf("got %v", err)
	}
	if len(reg.commits) != 0 {
		t.Error("it committed something")
	}
}

// A removal without a reason is refused. The ignored list carries it, and an entry saying
// only that somebody decided something is what that list must not hold.
func TestController_Remove_noReason(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{inFlight: map[string]struct{}{}, index: index()}
	err := New(reg, "main").Remove(t.Context(), logger(), &Input{PkgName: "foo/bar"})
	if !errors.Is(err, errReasonRequired) {
		t.Fatalf("got %v", err)
	}
	if len(reg.commits) != 0 {
		t.Error("it committed something")
	}
}
