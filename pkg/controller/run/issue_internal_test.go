package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/issuemark"
	"github.com/aquaproj/ar2/pkg/verify"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

// fakeIssues stands in for the registry's issues.
type fakeIssues struct {
	open     []*gogithub.Issue
	created  []gogithub.CreateIssueRequest
	comments map[int][]string
}

func (f *fakeIssues) ListByRepo(_ context.Context, _, _ string, _ *gogithub.IssueListByRepoOptions) ([]*gogithub.Issue, *gogithub.Response, error) {
	return f.open, &gogithub.Response{}, nil
}

func (f *fakeIssues) Create(_ context.Context, _, _ string, issue gogithub.CreateIssueRequest) (*gogithub.Issue, *gogithub.Response, error) {
	f.created = append(f.created, issue)
	number := len(f.created)
	return &gogithub.Issue{Number: &number}, &gogithub.Response{}, nil
}

func (f *fakeIssues) CreateComment(_ context.Context, _, _ string, number int, comment gogithub.IssueCommentRequest) (*gogithub.IssueComment, *gogithub.Response, error) {
	if f.comments == nil {
		f.comments = map[int][]string{}
	}
	f.comments[number] = append(f.comments[number], comment.Body)
	return &gogithub.IssueComment{}, &gogithub.Response{}, nil
}

func excludedVersions() []*version {
	return []*version{{
		Version: "v1.2.3",
		Excluded: []*verify.Excluded{{
			Asset: &generate.Asset{
				OS: "windows", Arch: "amd64", Format: "zip",
				Asset: "a_windows_amd64.zip", Type: "github_release",
				RepoOwner: "an", RepoName: "owner",
			},
			Reason: "download the asset: status code 404",
		}},
	}}
}

// The issue says which environment went and why, and carries the entry it went as.
func TestExcludedBody(t *testing.T) {
	t.Parallel()
	body, err := excludedBody("an/owner", []*excludedVersion{
		{version: "v1.2.3", excluded: excludedVersions()[0].Excluded},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"an/owner", "v1.2.3", "windows/amd64", "status code 404",
		"ar2 test --os <os> --arch <arch>", "ar2 regenerate an/owner <version>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue doesn't say %q:\n%s", want, body)
		}
	}
	// The block is a registry.json of what went, which is what ar2 test takes.
	start := strings.Index(body, "```json\n")
	end := strings.Index(body[start:], "```\n\n</details>")
	if start < 0 || end < 0 {
		t.Fatalf("the issue carries no entries:\n%s", body)
	}
	reg := &generate.Registry{}
	if err := json.Unmarshal([]byte(body[start+len("```json\n"):start+end]), reg); err != nil {
		t.Fatalf("what it carries isn't a registry.json: %v", err)
	}
	if len(reg.Assets) != 1 || reg.Assets[0].Asset != "a_windows_amd64.zip" {
		t.Errorf("what it carries is %+v", reg.Assets)
	}
}

// One issue per package: the first version opens it and the next comments on it, so the
// history of a cause is in one place.
func TestController_reportExcluded(t *testing.T) {
	t.Parallel()
	issues := &fakeIssues{}
	c := New(ghClient(t), nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
	c.UseIssues(issues, "aquaproj", "aqua-registry-g2")

	c.reportExcluded(context.Background(), discardLogger(), "an/owner", excludedVersions())
	if len(issues.created) != 1 {
		t.Fatalf("%d issues were opened", len(issues.created))
	}
	if got := issues.created[0].Title; got != excludedTitle("an/owner") {
		t.Errorf("the issue is called %q", got)
	}
	if len(issues.created[0].Labels) != 1 || issues.created[0].Labels[0] != ExcludedLabel {
		t.Errorf("the labels are %v", issues.created[0].Labels)
	}

	title := excludedTitle("an/owner")
	number := 7
	issues.open = []*gogithub.Issue{{Number: &number, Title: &title}}
	c.reportExcluded(context.Background(), discardLogger(), "an/owner", excludedVersions())
	if len(issues.created) != 1 {
		t.Errorf("%d issues were opened where one was there already", len(issues.created))
	}
	if len(issues.comments[number]) != 1 {
		t.Errorf("the issue was commented on %d times", len(issues.comments[number]))
	}
}

// A package that lost no environment says nothing, and neither does a run with nowhere to
// say it.
func TestController_reportExcluded_nothingToSay(t *testing.T) {
	t.Parallel()
	issues := &fakeIssues{}
	c := New(ghClient(t), nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
	c.UseIssues(issues, "aquaproj", "aqua-registry-g2")
	c.reportExcluded(context.Background(), discardLogger(), "an/owner", []*version{{Version: "v1.2.3"}})
	if len(issues.created) != 0 || len(issues.comments) != 0 {
		t.Errorf("%d opened, %d commented", len(issues.created), len(issues.comments))
	}

	quiet := New(ghClient(t), nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
	quiet.reportExcluded(context.Background(), discardLogger(), "an/owner", excludedVersions())
}

// A version a check of integrity stopped is said in an issue of its own kind, beside the
// package's issue about environments, and the next one comments on it.
func TestController_reportTampered(t *testing.T) {
	t.Parallel()
	issues := &fakeIssues{}
	c := New(ghClient(t), nil, nil, &fakeRegistry{}, failingAutoMerger{}, nil, nil)
	c.UseIssues(issues, "aquaproj", "aqua-registry-g2")
	excluded := excludedTitle("an/owner")
	other := 3
	issues.open = []*gogithub.Issue{{Number: &other, Title: &excluded}}

	stopped := []*tamperedVersion{{version: "v1.2.3", reason: "the digest of a.tar.gz doesn't match the downloaded asset"}}
	c.reportTampered(context.Background(), discardLogger(), &Input{}, "an/owner", stopped)
	if len(issues.created) != 1 {
		t.Fatalf("%d issues were opened", len(issues.created))
	}
	checkTamperedIssue(t, issues.created[0])
	if len(issues.comments[other]) != 0 {
		t.Error("the issue about environments was commented on")
	}

	title := tamperedTitle("an/owner")
	number := 9
	issues.open = append(issues.open, &gogithub.Issue{Number: &number, Title: &title})
	c.reportTampered(context.Background(), discardLogger(), &Input{}, "an/owner", stopped)
	if len(issues.created) != 1 || len(issues.comments[number]) != 1 {
		t.Errorf("%d opened, %d comments on the open one", len(issues.created), len(issues.comments[number]))
	}

	c.reportTampered(context.Background(), discardLogger(), &Input{}, "an/owner", nil)
	if len(issues.created) != 1 || len(issues.comments[number]) != 1 {
		t.Error("nothing stopped and something was said")
	}
}

// An environment nothing was picked for is said in the issue, and left out of the entries
// ar2 test takes, since there is no asset to try.
func TestExcludedBody_nothingPicked(t *testing.T) {
	t.Parallel()
	body, err := excludedBody("an/owner", []*excludedVersion{{
		version: "v1.2.3",
		excluded: []*verify.Excluded{{
			Asset:  &generate.Asset{OS: "linux", Arch: "amd64", Type: "github_release"},
			Reason: "assets of an environment can't be told apart by their names: a.tgz, a-rocm.tgz",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "- `linux/amd64`: assets of an environment can't be told apart") {
		t.Errorf("the issue doesn't say what went:\n%s", body)
	}
	if !strings.Contains(body, "\"assets\": []") {
		t.Errorf("an entry with no asset is in what ar2 test takes:\n%s", body)
	}
}

func checkTamperedIssue(t *testing.T, created gogithub.CreateIssueRequest) {
	t.Helper()
	if created.Title != tamperedTitle("an/owner") {
		t.Errorf("the issue is called %q", created.Title)
	}
	if len(created.Labels) != 1 || created.Labels[0] != TamperedLabel {
		t.Errorf("the labels are %v", created.Labels)
	}
	for _, want := range []string{"v1.2.3", "doesn't match the downloaded asset", "an/owner"} {
		if !strings.Contains(created.GetBody(), want) {
			t.Errorf("the issue doesn't say %q:\n%s", want, created.GetBody())
		}
	}
}

// What an issue is about is written into it, for ar2 close-issues to read back.
func TestIssueRecords(t *testing.T) {
	t.Parallel()
	body, err := excludedBody("an/owner", []*excludedVersion{
		{version: "v1.2.3", excluded: excludedVersions()[0].Excluded},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []*issuemark.Record{{
		Kind: issuemark.KindMissingEnvironment, Package: "an/owner",
		Versions: map[string][]string{"v1.2.3": {"windows/amd64"}},
	}}
	if diff := cmp.Diff(want, issuemark.Parse(body)); diff != "" {
		t.Errorf("the issue about environments (-want +got):\n%s", diff)
	}
	body, err = tamperedBody("an/owner", []*tamperedVersion{{version: "v1.2.3", reason: "a digest"}})
	if err != nil {
		t.Fatal(err)
	}
	want = []*issuemark.Record{{Kind: issuemark.KindIntegrity, Package: "an/owner", Versions: map[string][]string{"v1.2.3": nil}}}
	if diff := cmp.Diff(want, issuemark.Parse(body)); diff != "" {
		t.Errorf("the issue about integrity (-want +got):\n%s", diff)
	}
}
