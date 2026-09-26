package index

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// fakeRegistry stands in for aqua-registry-g2 and records what was written to it.
type fakeRegistry struct {
	index   *aquag2.Index
	configs map[string]*aquag2.Config
	openPR  *gogithub.PullRequest
	readRef string
	// files is what the repository holds, by path, so that a run can be told a file
	// is already what it renders as.
	files      map[string]string
	committed  []*g2.File
	parent     string
	createdPRs int
	configRead []string
}

func (f *fakeRegistry) Index(_ context.Context, ref string) (*aquag2.Index, error) {
	f.readRef = ref
	if f.index == nil {
		return &aquag2.Index{}, nil
	}
	return f.index, nil
}

func (f *fakeRegistry) File(_ context.Context, _, path string) (string, error) {
	return f.files[path], nil
}

func (f *fakeRegistry) Config(_ context.Context, pkgName string) (*aquag2.Config, error) {
	f.configRead = append(f.configRead, pkgName)
	return f.configs[pkgName], nil
}

func (f *fakeRegistry) BranchSHA(_ context.Context, _ string) (string, error) {
	return "parent-sha", nil
}

func (f *fakeRegistry) Commit(_ context.Context, _, parent, _ string, files []*g2.File) error {
	f.parent = parent
	f.committed = files
	return nil
}

func (f *fakeRegistry) IndexPullRequest(_ context.Context) (*gogithub.PullRequest, error) {
	return f.openPR, nil
}

func (f *fakeRegistry) CreateIndexPullRequest(_ context.Context, _, _, _ string) (*gogithub.PullRequest, error) {
	f.createdPRs++
	return &gogithub.PullRequest{Number: new(1), NodeID: new("PR_node")}, nil
}

// fakeDefinitions is what every package branch holds, as the branch reader returns it.
type fakeDefinitions struct {
	files map[string]string
	read  int
}

func (f *fakeDefinitions) Files(_ context.Context, _ *slog.Logger, _, _ string) (map[string]string, error) {
	f.read++
	return f.files, nil
}

// definitions renders each package's definition onto its branch, the way the repository
// holds it.
func definitions(t *testing.T, configs map[string]*aquag2.Config) *fakeDefinitions {
	t.Helper()
	files := make(map[string]string, len(configs))
	for pkgName, cfg := range configs {
		if cfg == nil {
			// A branch with nothing generated onto it yet holds no definition at all,
			// which the reader leaves out rather than reporting.
			continue
		}
		b, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		files[aquag2.BranchName(pkgName)] = string(b)
	}
	return &fakeDefinitions{files: files}
}

func config(description string) *aquag2.Config {
	return &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{Description: description}}
}

func logger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestController_AddPackage(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	err := New(reg, &fakeMerger{}, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool"))
	if err != nil {
		t.Fatal(err)
	}
	if reg.createdPRs != 1 {
		t.Errorf("opened %d pull requests, want 1", reg.createdPRs)
	}
	// The catalogue and the table of other names go in one commit, so that they
	// can't describe different registries.
	if len(reg.committed) != 2 {
		t.Fatalf("committed %d files, want the catalogue and the aliases", len(reg.committed))
	}
	if reg.committed[0].Path != g2.IndexFileName || reg.committed[1].Path != aquag2.AliasesFileName {
		t.Fatalf("committed %s and %s", reg.committed[0].Path, reg.committed[1].Path)
	}
	if !strings.Contains(reg.committed[0].Content, "cli/cli") {
		t.Errorf("the catalogue doesn't hold the package:\n%s", reg.committed[0].Content)
	}
	// The definition came with the call, so no branch is read for it.
	if len(reg.configRead) != 0 {
		t.Errorf("read %v, want nothing", reg.configRead)
	}
}

// A package already in the catalogue costs nothing: nothing is committed and no pull
// request is opened.
func TestController_AddPackage_alreadyThere(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{index: &aquag2.Index{Packages: []*aquag2.IndexPackage{{Name: "cli/cli"}}}}
	err := New(reg, &fakeMerger{}, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool"))
	if err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}

// The catalogue is made to say what the branches say: a package it doesn't have is added,
// and an entry whose definition says something else now is read out of it again.
func TestController_Sync(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "what it said when it arrived"},
	}}
	reg := &fakeRegistry{index: index, files: rendered(t, index)}
	defs := definitions(t, map[string]*aquag2.Config{
		"cli/cli":               config("what the definition says now"),
		"suzuki-shunsuke/tfcmt": config("Fork of tfnotify"),
	})

	if err := New(reg, &fakeMerger{}, "main", defs).Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if defs.read != 1 {
		t.Errorf("read the branches %d times, want once", defs.read)
	}
	if len(reg.committed) == 0 {
		t.Fatal("committed nothing")
	}
	got, err := aquag2.ReadIndex(strings.NewReader(reg.committed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	want := []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "what the definition says now"},
		{Name: "suzuki-shunsuke/tfcmt", Description: "Fork of tfnotify"},
	}
	if diff := cmp.Diff(want, got.Packages); diff != "" {
		t.Errorf("the catalogue is wrong (-want +got):\n%s", diff)
	}
	if reg.createdPRs != 1 {
		t.Errorf("opened %d pull requests, want 1", reg.createdPRs)
	}
}

// A catalogue already saying what every branch says is committed nothing, which is what
// the reconciliation comes to on almost every run.
func TestController_Sync_upToDate(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "GitHub's official command line tool"},
	}}
	reg := &fakeRegistry{index: index, files: rendered(t, index)}
	defs := definitions(t, map[string]*aquag2.Config{
		"cli/cli": config("GitHub's official command line tool"),
	})

	if err := New(reg, &fakeMerger{}, "main", defs).Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}

// An entry is not removed because a branch has no definition. It is a package waiting for
// the pull request that brings one, which a run has already added to the catalogue, and
// removing it would undo that.
func TestController_Sync_keepsWhatHasNoDefinition(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "GitHub's official command line tool"},
		{Name: "sst/opencode"},
	}}
	reg := &fakeRegistry{index: index, files: rendered(t, index)}
	defs := definitions(t, map[string]*aquag2.Config{
		"cli/cli": config("GitHub's official command line tool"),
	})

	if err := New(reg, &fakeMerger{}, "main", defs).Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
}

// An open pull request is added to rather than replaced, and the catalogue is read
// from it. Reading the default branch would make this run undo the last one.
func TestController_AddPackage_addsToTheOpenPullRequest(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		openPR: &gogithub.PullRequest{Number: new(7)},
		index:  &aquag2.Index{Packages: []*aquag2.IndexPackage{{Name: "aquaproj/aqua"}}},
	}
	err := New(reg, &fakeMerger{}, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(g2.IndexBranch, reg.readRef); diff != "" {
		t.Errorf("the catalogue was read from the wrong ref (-want +got):\n%s", diff)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
	// Both packages survive: the one waiting to merge and the one just added.
	for _, name := range []string{"aquaproj/aqua", "cli/cli"} {
		if !strings.Contains(reg.committed[0].Content, name) {
			t.Errorf("the catalogue lost %s:\n%s", name, reg.committed[0].Content)
		}
	}
}

// A branch with nothing generated onto it yet has no definition to describe the package
// with. The run that writes one brings it here.
func TestController_Sync_noConfigYet(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{files: rendered(t, &aquag2.Index{})}
	defs := definitions(t, map[string]*aquag2.Config{"cli/cli": nil})
	if err := New(reg, &fakeMerger{}, "main", defs).Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if reg.committed != nil {
		t.Errorf("committed %+v, want nothing", reg.committed)
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests, want none", reg.createdPRs)
	}
}

// A catalogue whose packages are all there, but one of whose files the registry was
// never written with, is committed anyway. That is what a file added to what a run
// maintains looks like until a run reaches it, and counting the packages it added would
// never notice.
func TestController_Sync_fileMissing(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "GitHub's official command line tool"},
	}}
	files := rendered(t, index)
	delete(files, aquag2.AliasesFileName)
	reg := &fakeRegistry{index: index, files: files}
	defs := definitions(t, map[string]*aquag2.Config{
		"cli/cli": config("GitHub's official command line tool"),
	})

	if err := New(reg, &fakeMerger{}, "main", defs).Sync(t.Context(), logger()); err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) != 1 || reg.committed[0].Path != aquag2.AliasesFileName {
		t.Fatalf("committed %+v, want the file that was missing", reg.committed)
	}
	if reg.createdPRs != 1 {
		t.Errorf("opened %d pull requests, want 1", reg.createdPRs)
	}
}

// rendered is what the repository holds when it holds a catalogue as this renders it.
func rendered(t *testing.T, index *aquag2.Index) map[string]string {
	t.Helper()
	files, err := catalogue(index)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(files))
	for _, file := range files {
		out[file.Path] = file.Content
	}
	return out
}

type fakeMerger struct {
	ids []string
	err error
}

func (m *fakeMerger) EnableAutoMerge(_ context.Context, pullRequestID string) error {
	m.ids = append(m.ids, pullRequestID)
	return m.err
}

// The catalogue holds nothing anyone decides, so it merges itself once the checks on
// main say aqua can read it.
func TestController_AddPackage_autoMerge(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	merger := &fakeMerger{}
	if err := New(reg, merger, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"PR_node"}; len(merger.ids) != 1 || merger.ids[0] != want[0] {
		t.Fatalf("turned over %v, want %v", merger.ids, want)
	}
}

// A pull request that is open and correct isn't thrown away because it couldn't be
// turned over; it waits for someone instead.
func TestController_AddPackage_autoMergeFails(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	merger := &fakeMerger{err: errors.New("auto-merge is off for this repository")}
	if err := New(reg, merger, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool")); err != nil {
		t.Fatal(err)
	}
	if reg.createdPRs != 1 {
		t.Fatalf("opened %d pull requests, want 1", reg.createdPRs)
	}
}

// Without one, the catalogue's pull requests wait for someone.
func TestController_AddPackage_noAutoMerger(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	if err := New(reg, nil, "main", nil).AddPackage(t.Context(), logger(), "cli/cli",
		config("GitHub's official command line tool")); err != nil {
		t.Fatal(err)
	}
	if reg.createdPRs != 1 {
		t.Fatalf("opened %d pull requests, want 1", reg.createdPRs)
	}
}

// The table of other names is inverted from the catalogue, so a package that carries an
// alias produces one a name can be looked up in.
//
// It is what resolves the old name for somebody whose aqua.yaml still says it, and this
// registry addresses a package by name: nothing can be fetched before the name is
// resolved.
func TestController_AddPackage_aliases(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{}
	cfg := config("The AI coding agent built for the terminal")
	cfg.Aliases = []*aquaregistry.Alias{{Name: "sst/opencode"}}
	if err := New(reg, &fakeMerger{}, "main", nil).AddPackage(t.Context(), logger(),
		"anomalyco/opencode", cfg); err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) != 2 {
		t.Fatalf("committed %d files", len(reg.committed))
	}
	aliases, err := aquag2.ReadAliases(strings.NewReader(reg.committed[1].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got := aliases.Resolve("sst/opencode"); got != "anomalyco/opencode" {
		t.Errorf("the old name resolves to %q", got)
	}
}

// A renamed package is listed under its new name and stops being listed under the old
// one, in one commit. A catalogue holding the old name as a package and the new one as a
// package whose alias is that name says two things about one name.
func TestController_Rename(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli"},
		{Name: "sst/opencode"},
	}}
	reg := &fakeRegistry{index: index, files: rendered(t, index)}
	cfg := config("The AI coding agent built for the terminal")
	cfg.Aliases = []*aquaregistry.Alias{{Name: "sst/opencode"}}

	if err := New(reg, &fakeMerger{}, "main", nil).Rename(t.Context(), logger(),
		"sst/opencode", "anomalyco/opencode", cfg); err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) == 0 {
		t.Fatal("committed nothing")
	}
	got, err := aquag2.ReadIndex(strings.NewReader(reg.committed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(got.Packages))
	for _, pkg := range got.Packages {
		names = append(names, pkg.Name)
	}
	if diff := cmp.Diff([]string{"anomalyco/opencode", "cli/cli"}, names); diff != "" {
		t.Errorf("the catalogue is wrong (-want +got):\n%s", diff)
	}
	// And the table beside it resolves the old name, which is the point of the alias.
	aliases, err := aquag2.ReadAliases(strings.NewReader(reg.committed[1].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got := aliases.Resolve("sst/opencode"); got != "anomalyco/opencode" {
		t.Errorf("the old name resolves to %q", got)
	}
}

// An entry follows the definition it was made from. A definition edited after the
// package arrived left the catalogue describing the package as it was, and nothing
// noticed: the reconciliation asks what the catalogue is missing, and this isn't.
func TestController_Refresh(t *testing.T) {
	t.Parallel()
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "what it said when it arrived"},
		{Name: "sst/opencode"},
	}}
	cfg := config("what the definition says now")
	cfg.Aliases = []*aquaregistry.Alias{{Name: "github/hub"}}
	reg := &fakeRegistry{
		index:   index,
		files:   rendered(t, index),
		configs: map[string]*aquag2.Config{"cli/cli": cfg},
	}

	if err := New(reg, &fakeMerger{}, "main", nil).Refresh(t.Context(), logger(), []string{"cli/cli"}); err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) == 0 {
		t.Fatal("committed nothing")
	}
	got, err := aquag2.ReadIndex(strings.NewReader(reg.committed[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	want := []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "what the definition says now", Aliases: []string{"github/hub"}},
		{Name: "sst/opencode"},
	}
	if diff := cmp.Diff(want, got.Packages); diff != "" {
		t.Errorf("the catalogue is wrong (-want +got):\n%s", diff)
	}
	// The table beside it is rendered from the same entries, which is why an alias
	// added by hand reaches aqua only once this has run.
	aliases, err := aquag2.ReadAliases(strings.NewReader(reg.committed[1].Content))
	if err != nil {
		t.Fatal(err)
	}
	if got := aliases.Resolve("github/hub"); got != "cli/cli" {
		t.Errorf("the alias resolves to %q", got)
	}
}

// Refreshing a package whose entry is already what its definition says commits
// nothing. It is what pointing the command at a package to find out comes to.
func TestController_Refresh_unchanged(t *testing.T) {
	t.Parallel()
	cfg := config("the same as ever")
	index := &aquag2.Index{Packages: []*aquag2.IndexPackage{
		{Name: "cli/cli", Description: "the same as ever"},
	}}
	reg := &fakeRegistry{
		index:   index,
		files:   rendered(t, index),
		configs: map[string]*aquag2.Config{"cli/cli": cfg},
	}
	if err := New(reg, &fakeMerger{}, "main", nil).Refresh(t.Context(), logger(), []string{"cli/cli"}); err != nil {
		t.Fatal(err)
	}
	if len(reg.committed) != 0 {
		t.Errorf("committed %d files", len(reg.committed))
	}
	if reg.createdPRs != 0 {
		t.Errorf("opened %d pull requests", reg.createdPRs)
	}
}

// A name that has no definition on its branch is said rather than skipped: named
// explicitly, it is a name that is wrong rather than a package waiting for its first
// run.
func TestController_Refresh_noDefinition(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{configs: map[string]*aquag2.Config{}}
	err := New(reg, &fakeMerger{}, "main", nil).Refresh(t.Context(), logger(), []string{"cli/cli"})
	if !errors.Is(err, errNoDefinition) {
		t.Fatalf("a package without a definition should be refused, got %v", err)
	}
	if len(reg.committed) != 0 {
		t.Errorf("committed %d files", len(reg.committed))
	}
}

// What the commit says is the difference between an entry that is new and one that was
// read again, which the entries themselves don't say.
func TestCommitMessage(t *testing.T) {
	t.Parallel()
	cli := &aquag2.IndexPackage{Name: "cli/cli"}
	opencode := &aquag2.IndexPackage{Name: "sst/opencode"}
	tests := []struct {
		name   string
		change *change
		want   string
	}{
		{
			name:   "one added",
			change: &change{added: []*aquag2.IndexPackage{cli}},
			want:   "feat(cli/cli): add the package to the index",
		},
		{
			name:   "two added",
			change: &change{added: []*aquag2.IndexPackage{cli, opencode}},
			want:   "feat: add 2 packages to the index",
		},
		{
			name:   "one updated",
			change: &change{updated: []*aquag2.IndexPackage{cli}},
			want:   "fix(cli/cli): update the index entry",
		},
		{
			name:   "two updated",
			change: &change{updated: []*aquag2.IndexPackage{cli, opencode}},
			want:   "fix: update the index entries of 2 packages",
		},
		{
			name:   "both",
			change: &change{added: []*aquag2.IndexPackage{cli}, updated: []*aquag2.IndexPackage{opencode}},
			want:   "feat: add 1 packages to the index and update 1",
		},
		{
			name:   "neither",
			change: &change{},
			want:   "chore: write the catalogue's files as they are rendered now",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := commitMessage(tt.change); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
