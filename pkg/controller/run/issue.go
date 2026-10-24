package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/issuemark"
	"github.com/aquaproj/ar2/pkg/verify"
	gogithub "github.com/google/go-github/v93/github"
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
	if err := c.say(ctx, logger, pkgName, excludedTitle(pkgName), ExcludedLabel, body); err != nil {
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

// say adds to the package's issue of the kind the label names, or opens it.
func (c *Controller) say(ctx context.Context, logger *slog.Logger, pkgName, title, label, body string) error {
	issue, found, err := c.openIssue(ctx, title, label)
	if err != nil {
		return err
	}
	if !found {
		created, _, err := c.issues.Create(ctx, c.issuesOwner, c.issuesRepo, gogithub.CreateIssueRequest{
			Title:  title,
			Body:   &body,
			Labels: []string{label},
		})
		if err != nil {
			return fmt.Errorf("open an issue: %w", err)
		}
		logger.Info("opened an issue", "package", pkgName, "label", label, "issue", created.GetNumber())
		return nil
	}
	if _, _, err := c.issues.CreateComment(ctx, c.issuesOwner, c.issuesRepo, issue.GetNumber(),
		gogithub.IssueCommentRequest{Body: body}); err != nil {
		return fmt.Errorf("comment on issue %d: %w", issue.GetNumber(), err)
	}
	logger.Info("commented on the issue", "package", pkgName, "label", label, "issue", issue.GetNumber())
	return nil
}

// openIssue is the package's open issue of the given title and label, and whether it has one.
func (c *Controller) openIssue(ctx context.Context, title, label string) (*gogithub.Issue, bool, error) {
	opts := &gogithub.IssueListByRepoOptions{Labels: []string{label}, State: "open"}
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
	marker, err := issuemark.Marker(excludedRecord(pkgName, versions))
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	b.WriteString("\nThis issue is closed once every environment above is in the registry again.\n\n" + marker + "\n")
	return b.String(), nil
}

// excludedRecord is what an issue about environments left out is about, for ar2 close-issues to
// read back.
func excludedRecord(pkgName string, versions []*excludedVersion) *issuemark.Record {
	r := &issuemark.Record{Kind: issuemark.KindMissingEnvironment, Package: pkgName, Versions: map[string][]string{}}
	for _, v := range versions {
		for _, e := range v.excluded {
			r.Versions[v.version] = append(r.Versions[v.version], e.Environment())
		}
	}
	return r
}

// marshalExcluded renders the entries that went as a registry.json, indented because this
// one is read by eye as well as by a command.
func marshalExcluded(excluded []*verify.Excluded) (string, error) {
	reg := &generate.Registry{Assets: make([]*generate.Asset, 0, len(excluded))}
	for _, e := range excluded {
		// An environment nothing was picked for has no asset, and nothing for ar2 test to
		// try. The line above the blocks already says why it went.
		if e.Asset == nil || (e.Asset.Asset == "" && e.Asset.URL == "") {
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

// TamperedLabel is what the issues about a release that failed a check of its integrity are
// found by.
const TamperedLabel = "integrity"

// tamperedVersion is one version that was stopped, and the check it failed.
type tamperedVersion struct {
	version string
	reason  string
}

// reportTampered says, as an issue, which versions were stopped because the release failed a
// check of its integrity.
//
// Such a version isn't published for any environment, and a run tries it again every time
// because it is still missing, so without the issue the only trace of it is a line in each
// run's summary. What stopped it is either a release that isn't what it says -- an asset
// replaced after it was published, a signature that doesn't verify -- or a definition naming
// the wrong signer, and telling the two apart takes a person. One issue per package,
// commented on again, the same way an environment left out is.
//
// A run writing files instead of opening pull requests says nothing either.
func (c *Controller) reportTampered(ctx context.Context, logger *slog.Logger, input *Input, pkgName string, versions []*tamperedVersion) {
	if c.issues == nil || input.SkipPR || len(versions) == 0 {
		return
	}
	body, err := tamperedBody(pkgName, versions)
	if err != nil {
		logger.Warn("failed to say which versions were stopped", "error", err.Error())
		return
	}
	if err := c.say(ctx, logger, pkgName, tamperedTitle(pkgName), TamperedLabel, body); err != nil {
		logger.Warn("failed to report the versions a check of integrity stopped",
			"package", pkgName, "error", err.Error())
	}
}

// tamperedTitle is what the package's issue about integrity is called, and how it is found
// again.
func tamperedTitle(pkgName string) string {
	return pkgName + ": a release failed a check of its integrity"
}

// tamperedBody says which versions were stopped and by what.
func tamperedBody(pkgName string, versions []*tamperedVersion) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "These versions of `%s` were not published for any environment, because the release "+
		"failed a check of its integrity:\n\n", pkgName)
	for _, v := range versions {
		fmt.Fprintf(&b, "- %s: %s\n", v.version, v.reason)
	}
	b.WriteString("\n")
	if run := runURL(); run != "" {
		fmt.Fprintf(&b, "Generated by %s\n\n", run)
	}
	b.WriteString("An asset whose digest disagrees with what GitHub reports for it was replaced after it " +
		"was published. A signature that doesn't verify is either the release not being what it says, or " +
		"the definition naming a signer the release isn't signed by. Which one it is has to be looked at: " +
		"the environments that did check out are no more trustworthy than the one that didn't, so none of " +
		"them is published until it is.\n\n")
	b.WriteString("Every run tries these versions again, since they are still missing. A definition fixed on " +
		"the package's branch is what the next run generates them from.\n")
	r := &issuemark.Record{Kind: issuemark.KindIntegrity, Package: pkgName, Versions: map[string][]string{}}
	for _, v := range versions {
		r.Versions[v.version] = nil
	}
	marker, err := issuemark.Marker(r)
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	b.WriteString("\nThis issue is closed once every version above is in the registry.\n\n" + marker + "\n")
	return b.String(), nil
}

// KeepsFailingLabel is what the issues about versions that keep failing for a reason that
// should pass are found by.
const KeepsFailingLabel = issuemark.KindTransientFailure

// reportKeepsFailing says, as an issue, which versions have failed failuresBeforeIssue runs in
// a row for a reason that should pass on another try.
//
// Such a failure is retried rather than reported, which is right for a server's bad minute.
// A version that never gets past it is a version the registry silently goes without, so once
// it has failed that many runs in a row it is said, once, the way an environment left out is.
func (c *Controller) reportKeepsFailing(ctx context.Context, logger *slog.Logger, input *Input, pkgName string, versions []*tamperedVersion) {
	if c.issues == nil || input.SkipPR || len(versions) == 0 {
		return
	}
	body, err := keepsFailingBody(pkgName, versions)
	if err != nil {
		logger.Warn("failed to say which versions keep failing", "error", err.Error())
		return
	}
	if err := c.say(ctx, logger, pkgName, keepsFailingTitle(pkgName), KeepsFailingLabel, body); err != nil {
		logger.Warn("failed to report the versions that keep failing", "package", pkgName, "error", err.Error())
	}
}

// keepsFailingTitle is what the package's issue about versions that keep failing is called,
// and how it is found again.
func keepsFailingTitle(pkgName string) string {
	return pkgName + ": a version keeps failing to generate"
}

// keepsFailingBody says which versions keep failing and with what.
func keepsFailingBody(pkgName string, versions []*tamperedVersion) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "These versions of `%s` have failed to generate %d runs in a row, each time for a reason "+
		"that should pass on another try -- a server error, a rate limit, a connection that failed:\n\n",
		pkgName, failuresBeforeIssue)
	for _, v := range versions {
		fmt.Fprintf(&b, "- %s: %s\n", v.version, v.reason)
	}
	b.WriteString("\n")
	if run := runURL(); run != "" {
		fmt.Fprintf(&b, "Generated by %s\n\n", run)
	}
	b.WriteString("A failure like this is retried by the next run rather than reported, which is right for a " +
		"bad minute. A version that never gets past it is one the registry goes without, so it is said here: " +
		"an asset a server always fails to serve, or a limit the run always meets at the same point, is " +
		"something a person has to look at. Every run still tries these versions.\n")
	r := &issuemark.Record{Kind: issuemark.KindTransientFailure, Package: pkgName, Versions: map[string][]string{}}
	for _, v := range versions {
		r.Versions[v.version] = nil
	}
	marker, err := issuemark.Marker(r)
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	b.WriteString("\nThis issue is closed once every version above is in the registry.\n\n" + marker + "\n")
	return b.String(), nil
}
