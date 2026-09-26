package state

import (
	"strings"
	"testing"
	"time"

	"github.com/aquaproj/ar2/pkg/state"
)

func filled() *state.State {
	s := state.New()
	s.UpdatedAt = time.Date(2026, 9, 27, 1, 2, 3, 0, time.UTC)
	s.Packages["cli/cli"] = &state.Package{
		RepoOwner: "cli", RepoName: "cli", Stars: 40000, Round: 3, CaughtUp: true,
		Versions: "0123456789abcdef0123", LastDeepCheck: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
	}
	s.Packages["anomalyco/opencode"] = &state.Package{
		RepoOwner: "anomalyco", RepoName: "opencode", Round: 2,
	}
	s.Packages["foo/bar"] = &state.Package{Round: 0}
	s.Renamed = map[string]string{"sst/opencode": "anomalyco/opencode"}
	return s
}

// The summary counts what nothing else can answer: the lap the order is on, how many are
// still waiting for it, and how many have never been reached or walked.
func TestSummary(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Summary(&b, filled()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"packages in the order            3",
		"holding every version swept      1",
		"no run has reached               1",
		"history never walked             2",
		"turns the order is on            0",
		"waiting for that turn            1",
		"names moved to another           1",
		"written                          2026-09-27T01:02:03Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary doesn't say %q:\n%s", want, got)
		}
	}
}

// An empty state says so rather than dividing by a registry that isn't there.
func TestSummary_empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Summary(&b, state.New()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "packages in the order            0") {
		t.Errorf("got:\n%s", b.String())
	}
}

// A package is answered for with what decides when it is generated.
func TestPackages(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Packages(&b, filled(), []string{"cli/cli"}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"cli/cli\n",
		"repository                     cli/cli",
		"stars                          40000",
		"turns                          3",
		"behind the order by            3",
		"holds every version swept      true",
		"history walked                 2026-09-20T00:00:00Z",
		"versions last swept            0123456789ab",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the package doesn't say %q:\n%s", want, got)
		}
	}
}

// A package whose old name was asked about says where it went, and the name it went to
// says it is also held under the old one: a configuration may still be asking for it.
func TestPackages_renamed(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Packages(&b, filled(), []string{"sst/opencode", "anomalyco/opencode"}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "moved to                       anomalyco/opencode") {
		t.Errorf("it doesn't say where the old name went:\n%s", got)
	}
	if !strings.Contains(got, "also held under                sst/opencode") {
		t.Errorf("it doesn't say what the new name answers for:\n%s", got)
	}
}

// A name the order doesn't hold is the answer to why nothing has been generated for it.
func TestPackages_notInTheOrder(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Packages(&b, filled(), []string{"nobody/nothing"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "not in the order") {
		t.Errorf("got:\n%s", b.String())
	}
}

// A package that has never been reached says so where a zero would read as a date.
func TestPackages_neverWalked(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Packages(&b, filled(), []string{"foo/bar"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "history walked                 never") {
		t.Errorf("got:\n%s", b.String())
	}
}
