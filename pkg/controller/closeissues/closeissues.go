// Package closeissues closes the issues ar2 opened once what they are about is resolved.
package closeissues

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/issuemark"
	gogithub "github.com/google/go-github/v92/github"
)

// Issues is the repository's issues.
type Issues interface {
	ListByRepo(ctx context.Context, owner, repo string, opts *gogithub.IssueListByRepoOptions) ([]*gogithub.Issue, *gogithub.Response, error)
	ListComments(ctx context.Context, owner, repo string, number int, opts *gogithub.IssueListCommentsOptions) ([]*gogithub.IssueComment, *gogithub.Response, error)
	CreateComment(ctx context.Context, owner, repo string, number int, comment gogithub.IssueCommentRequest) (*gogithub.IssueComment, *gogithub.Response, error)
	Update(ctx context.Context, owner, repo string, number int, issue gogithub.UpdateIssueRequest) (*gogithub.Issue, *gogithub.Response, error)
}

// Registry is what the registry serves for a version of a package.
type Registry interface {
	Version(ctx context.Context, pkgName, version string) (*aquag2.Registry, error)
}

// Controller closes the resolved issues.
type Controller struct {
	issues      Issues
	registry    Registry
	owner, repo string
	dryRun      bool
}

// New creates a Controller.
func New(issues Issues, registry Registry, owner, repo string) *Controller {
	return &Controller{issues: issues, registry: registry, owner: owner, repo: repo}
}

// DryRun makes Close say which issues it would close and close none.
func (c *Controller) DryRun() {
	c.dryRun = true
}

// labels are the issues ar2 opens about a package, one kind each.
var labels = []string{issuemark.KindMissingEnvironment, issuemark.KindIntegrity} //nolint:gochecknoglobals

// perPage is how many issues or comments one request lists.
const perPage = 100

// Close closes every open issue ar2 opened whose every version and environment is resolved.
//
// What an issue is about is read from the records ar2 wrote into it and into each comment it
// added, and asked of what the default branch serves: an environment left out is resolved once
// the version's file covers it again, or the version is no longer served at all, and a version
// a check of integrity stopped is resolved once it is served. An issue saying something nothing
// has resolved yet stays open, and so does one holding no record, which is one written before
// the records were.
func (c *Controller) Close(ctx context.Context, logger *slog.Logger) error {
	for _, label := range labels {
		issues, err := c.open(ctx, label)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if err := c.closeIfResolved(ctx, logger, issue); err != nil {
				return err
			}
		}
	}
	return nil
}

// open is the open issues of the label.
func (c *Controller) open(ctx context.Context, label string) ([]*gogithub.Issue, error) {
	opts := &gogithub.IssueListByRepoOptions{Labels: []string{label}, State: "open"}
	opts.ListOptions.PerPage = perPage
	var out []*gogithub.Issue
	for {
		issues, resp, err := c.issues.ListByRepo(ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list the open issues labelled %s: %w", label, err)
		}
		for _, issue := range issues {
			if !issue.IsPullRequest() {
				out = append(out, issue)
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.ListOptions.Page = resp.NextPage
	}
}

// closeIfResolved closes the issue when everything it is about is resolved.
func (c *Controller) closeIfResolved(ctx context.Context, logger *slog.Logger, issue *gogithub.Issue) error {
	logger = logger.With("issue", issue.GetNumber())
	records, err := c.records(ctx, issue)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		logger.Debug("the issue says nothing ar2 can check")
		return nil
	}
	pending, resolved, err := c.check(ctx, records)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		logger.Info("the issue is still about something", "unresolved", strings.Join(pending, ", "))
		return nil
	}
	logger.Info("everything the issue is about is resolved", "resolved", strings.Join(resolved, ", "))
	if c.dryRun {
		return nil
	}
	var body strings.Builder
	body.WriteString("Everything this issue was about is resolved, as the registry serves it now:\n\n")
	for _, line := range resolved {
		body.WriteString("- " + line + "\n")
	}
	body.WriteString("\nClosed by `ar2 close-issues`.\n")
	if _, _, err := c.issues.CreateComment(ctx, c.owner, c.repo, issue.GetNumber(), gogithub.IssueCommentRequest{Body: body.String()}); err != nil {
		return fmt.Errorf("comment on issue %d: %w", issue.GetNumber(), err)
	}
	if _, _, err := c.issues.Update(ctx, c.owner, c.repo, issue.GetNumber(), gogithub.UpdateIssueRequest{
		State:       new("closed"),
		StateReason: new("completed"),
	}); err != nil {
		return fmt.Errorf("close issue %d: %w", issue.GetNumber(), err)
	}
	return nil
}

// records is what the issue and every comment on it are about.
func (c *Controller) records(ctx context.Context, issue *gogithub.Issue) ([]*issuemark.Record, error) {
	records := issuemark.Parse(issue.GetBody())
	opts := &gogithub.IssueListCommentsOptions{}
	opts.PerPage = perPage
	for {
		comments, resp, err := c.issues.ListComments(ctx, c.owner, c.repo, issue.GetNumber(), opts)
		if err != nil {
			return nil, fmt.Errorf("list the comments on issue %d: %w", issue.GetNumber(), err)
		}
		for _, comment := range comments {
			records = append(records, issuemark.Parse(comment.GetBody())...)
		}
		if resp == nil || resp.NextPage == 0 {
			return records, nil
		}
		opts.Page = resp.NextPage
	}
}

// check says, as lines, which of what the records are about is unresolved and which resolved.
func (c *Controller) check(ctx context.Context, records []*issuemark.Record) ([]string, []string, error) {
	var pending, resolved []string
	for _, r := range records {
		for _, version := range slices.Sorted(maps.Keys(r.Versions)) {
			reg, err := c.registry.Version(ctx, r.Package, version)
			if err != nil {
				return nil, nil, fmt.Errorf("read %s %s: %w", r.Package, version, err)
			}
			p, done := judge(r, version, reg)
			pending = append(pending, p...)
			resolved = append(resolved, done...)
		}
	}
	return pending, resolved, nil
}

// judge says what of one version of a record is unresolved and what resolved, given the file the
// registry serves for it, which is nil when it serves none.
func judge(r *issuemark.Record, version string, reg *aquag2.Registry) ([]string, []string) {
	name := r.Package + " " + version
	switch r.Kind {
	case issuemark.KindIntegrity:
		if reg == nil {
			return []string{name}, nil
		}
		return nil, []string{name + " is published"}
	case issuemark.KindMissingEnvironment:
		if reg == nil {
			return nil, []string{name + " is no longer published"}
		}
		var pending, resolved []string
		for _, env := range r.Versions[version] {
			if covers(reg, env) {
				resolved = append(resolved, name+" covers "+env)
				continue
			}
			pending = append(pending, name+" "+env)
		}
		return pending, resolved
	}
	// A kind this ar2 doesn't know is nothing it can say is resolved.
	return []string{name}, nil
}

// covers says whether the file has an entry for the environment, as os/arch.
func covers(reg *aquag2.Registry, env string) bool {
	return slices.ContainsFunc(reg.Assets, func(a *aquag2.Asset) bool { return a.OS+"/"+a.Arch == env })
}
