package closeissues

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/issuemark"
	"github.com/google/go-cmp/cmp"
	gogithub "github.com/google/go-github/v92/github"
)

type fakeIssues struct {
	open     map[string][]*gogithub.Issue
	comments map[int][]string
	said     map[int][]string
	closed   []int
}

func (f *fakeIssues) ListByRepo(_ context.Context, _, _ string, opts *gogithub.IssueListByRepoOptions) ([]*gogithub.Issue, *gogithub.Response, error) {
	return f.open[opts.Labels[0]], &gogithub.Response{}, nil
}

func (f *fakeIssues) ListComments(_ context.Context, _, _ string, number int, _ *gogithub.IssueListCommentsOptions) ([]*gogithub.IssueComment, *gogithub.Response, error) {
	out := make([]*gogithub.IssueComment, 0, len(f.comments[number]))
	for _, body := range f.comments[number] {
		out = append(out, &gogithub.IssueComment{Body: new(body)})
	}
	return out, &gogithub.Response{}, nil
}

func (f *fakeIssues) CreateComment(_ context.Context, _, _ string, number int, comment gogithub.IssueCommentRequest) (*gogithub.IssueComment, *gogithub.Response, error) {
	if f.said == nil {
		f.said = map[int][]string{}
	}
	f.said[number] = append(f.said[number], comment.Body)
	return &gogithub.IssueComment{}, &gogithub.Response{}, nil
}

func (f *fakeIssues) Update(_ context.Context, _, _ string, number int, req gogithub.UpdateIssueRequest) (*gogithub.Issue, *gogithub.Response, error) {
	if req.GetState() == "closed" {
		f.closed = append(f.closed, number)
	}
	return &gogithub.Issue{}, &gogithub.Response{}, nil
}

// fakeRegistry serves, by "<package> <version>", the environments the version's file covers.
type fakeRegistry map[string][]string

func (f fakeRegistry) Version(_ context.Context, pkgName, version string) (*aquag2.Registry, error) {
	envs, ok := f[pkgName+" "+version]
	if !ok {
		return nil, nil //nolint:nilnil
	}
	reg := &aquag2.Registry{}
	for _, env := range envs {
		os, arch, _ := strings.Cut(env, "/")
		reg.Assets = append(reg.Assets, &aquag2.Asset{OS: os, Arch: arch})
	}
	return reg, nil
}

func issueOf(t *testing.T, number int, r *issuemark.Record) *gogithub.Issue {
	t.Helper()
	marker, err := issuemark.Marker(r)
	if err != nil {
		t.Fatal(err)
	}
	return &gogithub.Issue{Number: new(number), Body: new("prose\n\n" + marker)}
}

func markerOf(t *testing.T, r *issuemark.Record) string {
	t.Helper()
	marker, err := issuemark.Marker(r)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

// An issue is closed once everything it and its comments are about is resolved, and stays open
// while anything isn't, or when it says nothing ar2 can check.
func TestController_Close(t *testing.T) {
	t.Parallel()
	missing := func(versions map[string][]string) *issuemark.Record {
		return &issuemark.Record{Kind: issuemark.KindMissingEnvironment, Package: "kcp-dev/kcp", Versions: versions}
	}
	issues := &fakeIssues{
		open: map[string][]*gogithub.Issue{
			issuemark.KindMissingEnvironment: {
				// Resolved: v1 covers windows again, and so does v2's linux/arm64.
				issueOf(t, 1, missing(map[string][]string{"v1": {"windows/amd64"}})),
				// v3, which a comment adds, still lacks darwin/arm64.
				issueOf(t, 2, missing(map[string][]string{"v1": {"windows/amd64"}})),
				// Written before the records were.
				{Number: new(3), Body: new("prose only")},
				// v4's pull request hasn't merged, which resolves nothing.
				issueOf(t, 7, missing(map[string][]string{"v4": {"windows/amd64"}})),
			},
			issuemark.KindIntegrity: {
				issueOf(t, 4, &issuemark.Record{Kind: issuemark.KindIntegrity, Package: "foo/foo", Versions: map[string][]string{"v1": nil}}),
				issueOf(t, 5, &issuemark.Record{Kind: issuemark.KindIntegrity, Package: "foo/foo", Versions: map[string][]string{"v9": nil}}),
			},
			issuemark.KindTransientFailure: {
				issueOf(t, 6, &issuemark.Record{Kind: issuemark.KindTransientFailure, Package: "foo/foo", Versions: map[string][]string{"v1": nil}}),
			},
		},
		comments: map[int][]string{
			1: {markerOf(t, missing(map[string][]string{"v2": {"linux/arm64"}}))},
			2: {markerOf(t, missing(map[string][]string{"v3": {"darwin/arm64"}}))},
		},
	}
	reg := fakeRegistry{
		"kcp-dev/kcp v1": {"linux/amd64", "windows/amd64"},
		"kcp-dev/kcp v2": {"linux/amd64", "linux/arm64"},
		"kcp-dev/kcp v3": {"linux/amd64"},
		"foo/foo v1":     {"linux/amd64"},
	}
	if err := New(issues, reg, "aquaproj", "aqua-registry-g2").Close(context.Background(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int{1, 4, 6}, issues.closed); diff != "" {
		t.Errorf("closed (-want +got):\n%s", diff)
	}
	if len(issues.said[1]) != 1 || len(issues.said[2]) != 0 {
		t.Errorf("commented %v", issues.said)
	}
}

// A dry run closes nothing.
func TestController_Close_dryRun(t *testing.T) {
	t.Parallel()
	issues := &fakeIssues{open: map[string][]*gogithub.Issue{
		issuemark.KindIntegrity: {issueOf(t, 4, &issuemark.Record{Kind: issuemark.KindIntegrity, Package: "foo/foo", Versions: map[string][]string{"v1": nil}})},
	}}
	c := New(issues, fakeRegistry{"foo/foo v1": {"linux/amd64"}}, "aquaproj", "aqua-registry-g2")
	c.DryRun()
	if err := c.Close(context.Background(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if len(issues.closed) != 0 || len(issues.said) != 0 {
		t.Errorf("a dry run closed %v and said %v", issues.closed, issues.said)
	}
}
