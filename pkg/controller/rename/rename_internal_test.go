package rename

import (
	"context"
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// fakeRegistry records what a rename asked it to do.
type fakeRegistry struct {
	moved   bool
	from    string
	to      string
	configs map[string]*aquag2.Config
}

func (f *fakeRegistry) RenamePackage(_ context.Context, from, to string) (bool, error) {
	f.from, f.to = from, to
	return f.moved, nil
}

func (f *fakeRegistry) Config(_ context.Context, pkgName string) (*aquag2.Config, error) {
	return f.configs[pkgName], nil
}

// fakeCatalogue records the rename it was told about.
type fakeCatalogue struct {
	from  string
	to    string
	cfg   *aquag2.Config
	calls int
}

func (f *fakeCatalogue) Rename(_ context.Context, _ *slog.Logger, from, to string, cfg *aquag2.Config) error {
	f.from, f.to, f.cfg = from, to, cfg
	f.calls++
	return nil
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// The branch first, then the catalogue: a catalogue naming a branch that isn't there is a
// registry answering nothing for a package it says it has.
func TestController_Rename(t *testing.T) {
	t.Parallel()
	cfg := &aquag2.Config{PackageInfo: &aquaregistry.PackageInfo{
		RepoOwner: "anomalyco", RepoName: "opencode",
		Aliases: []*aquaregistry.Alias{{Name: "sst/opencode"}},
	}}
	reg := &fakeRegistry{moved: true, configs: map[string]*aquag2.Config{"anomalyco/opencode": cfg}}
	cat := &fakeCatalogue{}

	if err := New(reg, cat).Rename(t.Context(), discard(),
		"sst/opencode", "anomalyco/opencode"); err != nil {
		t.Fatal(err)
	}
	if reg.from != "sst/opencode" || reg.to != "anomalyco/opencode" {
		t.Errorf("moved %s to %s", reg.from, reg.to)
	}
	// The catalogue is given the definition read from the new name, which is the one
	// carrying the old name as an alias.
	if cat.cfg != cfg {
		t.Error("the catalogue wasn't given the renamed definition")
	}
	if cat.from != "sst/opencode" || cat.to != "anomalyco/opencode" {
		t.Errorf("listed %s as %s", cat.from, cat.to)
	}
}

// A branch already under the new name is a run that stopped before the catalogue, so the
// catalogue is still brought up to it.
func TestController_Rename_branchAlreadyThere(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{moved: false, configs: map[string]*aquag2.Config{
		"c/c": {PackageInfo: &aquaregistry.PackageInfo{RepoOwner: "c", RepoName: "c"}},
	}}
	cat := &fakeCatalogue{}
	if err := New(reg, cat).Rename(t.Context(), discard(), "a/a", "c/c"); err != nil {
		t.Fatal(err)
	}
	if cat.calls != 1 {
		t.Errorf("the catalogue was told %d times, want once", cat.calls)
	}
}

// Renaming a package to what it is called is nothing to do, and doing it carefully would
// list it twice and then remove it.
func TestController_Rename_sameName(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogue{}
	if err := New(&fakeRegistry{}, cat).Rename(t.Context(), discard(), "a/a", "a/a"); err == nil {
		t.Fatal("want an error")
	}
	if cat.calls != 0 {
		t.Error("the catalogue was touched")
	}
}
