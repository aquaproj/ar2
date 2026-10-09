package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v92/github"
)

// Issues is how an environment the registry can't offer is said somewhere a person or an
// agent will find it.
type Issues interface {
	ListByRepo(ctx context.Context, owner, repo string, opts *gogithub.IssueListByRepoOptions) ([]*gogithub.Issue, *gogithub.Response, error)
	Create(ctx context.Context, owner, repo string, issue gogithub.CreateIssueRequest) (*gogithub.Issue, *gogithub.Response, error)
	CreateComment(ctx context.Context, owner, repo string, number int, comment gogithub.IssueCommentRequest) (*gogithub.IssueComment, *gogithub.Response, error)
}

// ExcludedLabel is what the issues about an environment the registry can't offer are
// found by, which is the point of them: something else reads them.
const ExcludedLabel = "missing-environment"

// UseIssues says where to report the environments a version isn't offered for. Without it
// they are logged and summarized and nothing else.
func (c *Controller) UseIssues(issues Issues, owner, repo string) {
	c.issues = issues
	c.issuesOwner = owner
	c.issuesRepo = repo
}

// reportExcluded says, as an issue, which environments the registry isn't offering a
// version for.
//
// One issue per package, commented on again rather than opened again. What keeps an
// environment out is usually the package's rather than the version's -- a definition
// naming a file the archive stopped carrying, an upstream that stopped publishing an
// architecture -- so the history of it belongs in one place.
//
// It carries the entry that was left out. The file says nothing about that environment,
// which is the point, and the run's log expires, so the issue is the only place the
// attempt is written down. As a registry.json of just those entries, it is what
// 'ar2 test' takes, so whoever fixes the cause can show from the issue alone that it is
// fixed.
func (c *Controller) reportExcluded(ctx context.Context, logger *slog.Logger, pkgName string, versions []*version) {
	if c.issues == nil {
		return
	}
	excluded := make([]*excludedVersion, 0, len(versions))
	for _, v := range versions {
		if len(v.Excluded) == 0 {
			continue
		}
		excluded = append(excluded, &excludedVersion{version: v.Version, excluded: v.Excluded})
	}
	if len(excluded) == 0 {
		return
	}
	body, err := excludedBody(pkgName, excluded)
	if err != nil {
		logger.Warn("failed to say which environments were left out", "error", err.Error())
		return
	}
	if err := c.sayExcluded(ctx, logger, pkgName, body); err != nil {
		// The versions are published and the pull request is open. Failing the package
		// over the report would undo nothing and generate nothing.
		logger.Warn("failed to report the environments that were left out",
			"package", pkgName, "error", err.Error())
	}
}

// excludedVersion is one version and the environments it isn't offered for.
type excludedVersion struct {
	version  string
	excluded []*verify.Excluded
}

// sayExcluded adds to the package's issue, or opens it.
func (c *Controller) sayExcluded(ctx context.Context, logger *slog.Logger, pkgName, body string) error {
	title := excludedTitle(pkgName)
	issue, found, err := c.excludedIssue(ctx, title)
	if err != nil {
		return err
	}
	if !found {
		created, _, err := c.issues.Create(ctx, c.issuesOwner, c.issuesRepo, gogithub.CreateIssueRequest{
			Title:  title,
			Body:   &body,
			Labels: []string{ExcludedLabel},
		})
		if err != nil {
			return fmt.Errorf("open an issue: %w", err)
		}
		logger.Info("said which environments were left out", "package", pkgName, "issue", created.GetNumber())
		return nil
	}
	if _, _, err := c.issues.CreateComment(ctx, c.issuesOwner, c.issuesRepo, issue.GetNumber(),
		gogithub.IssueCommentRequest{Body: body}); err != nil {
		return fmt.Errorf("comment on issue %d: %w", issue.GetNumber(), err)
	}
	logger.Info("said which environments were left out", "package", pkgName, "issue", issue.GetNumber())
	return nil
}

// excludedIssue is the package's open issue, and whether it has one.
func (c *Controller) excludedIssue(ctx context.Context, title string) (*gogithub.Issue, bool, error) {
	opts := &gogithub.IssueListByRepoOptions{Labels: []string{ExcludedLabel}, State: "open"}
	opts.ListOptions.PerPage = issuesPerPage
	for {
		issues, resp, err := c.issues.ListByRepo(ctx, c.issuesOwner, c.issuesRepo, opts)
		if err != nil {
			return nil, false, fmt.Errorf("list the open issues: %w", err)
		}
		for _, issue := range issues {
			if issue.GetTitle() == title {
				return issue, true, nil
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return nil, false, nil
		}
		opts.ListOptions.Page = resp.NextPage
	}
}

// issuesPerPage is how many issues one request lists.
const issuesPerPage = 100

// excludedTitle is what the package's issue is called. It is how the issue is found again,
// so it says the package and nothing that changes.
func excludedTitle(pkgName string) string {
	return pkgName + ": the registry can't offer every environment"
}

// excludedBody says what was left out, why, and what was tried.
func excludedBody(pkgName string, versions []*excludedVersion) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "`%s` was generated without some of the environments it is meant to have. "+
		"The versions below are in the registry for the environments that worked, and not for these.\n\n", pkgName)
	for _, v := range versions {
		fmt.Fprintf(&b, "### %s\n\n", v.version)
		for _, e := range v.excluded {
			fmt.Fprintf(&b, "- `%s`: %s\n", e.Environment(), e.Reason)
		}
		b.WriteString("\n")
	}
	if run := runURL(); run != "" {
		fmt.Fprintf(&b, "Generated by %s\n\n", run)
	}
	b.WriteString("What was left out, as the entries it was left out as:\n\n")
	for _, v := range versions {
		content, err := marshalExcluded(v.excluded)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n```json\n%s```\n\n</details>\n\n", v.version, content)
	}
	b.WriteString("Each block is a registry.json of the entries that went, which is what `ar2 test` takes:\n\n")
	b.WriteString("```sh\nar2 test --os <os> --arch <arch> <the file>\n```\n\n")
	b.WriteString("So the cause can be shown fixed from here. Putting the environment back into the " +
		"registry is `ar2 regenerate " + pkgName + " <version>`, once it is.\n")
	return b.String(), nil
}

// marshalExcluded renders the entries that went as a registry.json, indented because this
// one is read by eye as well as by a command.
func marshalExcluded(excluded []*verify.Excluded) (string, error) {
	reg := &generate.Registry{Assets: make([]*generate.Asset, 0, len(excluded))}
	for _, e := range excluded {
		if e.Asset == nil {
			continue
		}
		reg.Assets = append(reg.Assets, e.Asset)
	}
	content, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal the entries that went: %w", err)
	}
	return string(content) + "\n", nil
}

// runURL is the workflow run that generated it, which is where the whole log is while it
// lasts. Empty outside Actions.
func runURL() string {
	server, repo, id := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || id == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, id)
}
