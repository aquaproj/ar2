// Package remove stops the registry serving a package.
//
// Which it doesn't do as a rule. What the registry promises is that a version it has
// published stays what it was, and a package it holds is a package somebody's
// configuration may name; removing one breaks that for whoever pinned it. The exceptions
// are a package that can't be installed by aqua whatever is generated for it, and
// malware, which goes without asking.
//
// So it isn't ignoring a package, which is a package the registry never took on and never
// published. It is three things at once: the registry stops generating the package, stops
// listing it, and stops holding what it generated. Any one of them alone leaves a state
// nobody meant -- an entry for a package that can't be fetched, or files nothing lists
// that the next run adds to -- so they are one command.
//
// What it cannot reach is a lock file. One that already holds the package carries the
// URL and the checksum of every file it needs, which is the point of it, and no change
// here takes that away. For malware, saying so where people will read it is the part
// that matters; this is only the part the registry can do.
package remove

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
)

// Registry is what a package is removed from.
type Registry interface {
	PackagesInFlight(ctx context.Context) (map[string]struct{}, error)
	File(ctx context.Context, ref, path string) (string, error)
	Index(ctx context.Context, ref string) (*aquag2.Index, error)
	BranchSHA(ctx context.Context, branch string) (string, error)
	VersionFiles(ctx context.Context, pkgName string) ([]string, error)
	Commit(ctx context.Context, branch, parent, message string, files []*g2.File) error
	CreatePullRequestFrom(ctx context.Context, head, base, title, body string) (*gogithub.PullRequest, error)
}

// Controller removes packages.
type Controller struct {
	g2         Registry
	baseBranch string
}

// New creates a Controller.
func New(registry Registry, baseBranch string) *Controller {
	return &Controller{g2: registry, baseBranch: baseBranch}
}

// Input is the package to stop serving.
type Input struct {
	PkgName string
	// Reason is why the registry stops serving it, in prose. It is what the ignored
	// list carries, and the next person to wonder why the package isn't here reads
	// that and nothing else.
	Reason string
}

// Remove opens the pull requests that stop the registry serving the package.
//
// Two of them, because they are on different branches: the registry's configuration and
// the catalogue are on the default branch, and what was generated is on the package's.
// The first has to merge first -- a package still in the order has its files generated
// again by the next run -- so it is opened first and the second says so.
func (c *Controller) Remove(ctx context.Context, logger *slog.Logger, in *Input) error {
	if in.Reason == "" {
		return errReasonRequired
	}
	logger = logger.With("package", in.PkgName)
	if err := c.noPullRequestInFlight(ctx, in.PkgName); err != nil {
		return err
	}

	pr, err := c.stopServing(ctx, logger, in)
	if err != nil {
		return err
	}
	return c.dropVersions(ctx, logger, in, pr)
}

// noPullRequestInFlight refuses a package that has a pull request open.
//
// Its head branch is where the versions are taken out, so going ahead would discard what
// that pull request holds -- and a pull request adding versions to a package that is
// being removed is a question to settle before removing it, not while.
func (c *Controller) noPullRequestInFlight(ctx context.Context, pkgName string) error {
	inFlight, err := c.g2.PackagesInFlight(ctx)
	if err != nil {
		return fmt.Errorf("list the packages with an open pull request: %w", err)
	}
	if _, ok := inFlight[g2.HeadBranchName(pkgName)]; ok {
		return fmt.Errorf("%w: %s", errPullRequestInFlight, pkgName)
	}
	return nil
}

// stopServing opens the pull request that takes the package out of the registry's
// configuration and its catalogue, and returns it.
func (c *Controller) stopServing(ctx context.Context, logger *slog.Logger, in *Input) (*gogithub.PullRequest, error) {
	files, err := c.mainFiles(ctx, in)
	if err != nil {
		return nil, err
	}
	parent, err := c.g2.BranchSHA(ctx, c.baseBranch)
	if err != nil {
		return nil, fmt.Errorf("get the branch to commit onto: %w", err)
	}
	title := fmt.Sprintf("fix(%s): stop serving the package", in.PkgName)
	branch := g2.RemoveBranchName(in.PkgName)
	if err := c.g2.Commit(ctx, branch, parent, title, files); err != nil {
		return nil, fmt.Errorf("commit the registry's configuration and catalogue: %w", err)
	}
	pr, err := c.g2.CreatePullRequestFrom(ctx, branch, c.baseBranch, title, stopBody(in))
	if err != nil {
		return nil, fmt.Errorf("open the pull request that stops the package being served: %w", err)
	}
	logger.Info("opened the pull request that stops the package being generated and listed",
		"number", pr.GetNumber())
	return pr, nil
}

// mainFiles are the default branch's files as they read once the package is gone: the
// registry's configuration with it ignored, and the catalogue without its entry.
func (c *Controller) mainFiles(ctx context.Context, in *Input) ([]*g2.File, error) {
	content, err := c.g2.File(ctx, c.baseBranch, g2.RegistryConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", g2.RegistryConfigFileName, err)
	}
	ignored, added, err := g2.IgnorePackage(content, in.PkgName, in.Reason)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error already names the file
	}

	index, err := c.g2.Index(ctx, c.baseBranch)
	if err != nil {
		return nil, fmt.Errorf("get the catalogue: %w", err)
	}
	index.Remove(in.PkgName)
	files, err := g2.CatalogueFiles(index)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	if !added {
		// Already ignored, which is a removal being finished rather than started.
		return files, nil
	}
	return append(files, &g2.File{Path: g2.RegistryConfigFileName, Content: ignored}), nil
}

// dropVersions opens the pull request that takes what was generated off the package's
// branch.
func (c *Controller) dropVersions(ctx context.Context, logger *slog.Logger, in *Input, stop *gogithub.PullRequest) error {
	paths, err := c.g2.VersionFiles(ctx, in.PkgName)
	if err != nil {
		return fmt.Errorf("list what the package's branch holds: %w", err)
	}
	if len(paths) == 0 {
		// Nothing was ever published, so ignoring it was the whole of it.
		logger.Info("the package's branch holds no generated file")
		return nil
	}
	files := make([]*g2.File, 0, len(paths))
	for _, path := range paths {
		files = append(files, &g2.File{Path: path, Deleted: true})
	}

	branch := g2.BranchName(in.PkgName)
	parent, err := c.g2.BranchSHA(ctx, branch)
	if err != nil {
		return fmt.Errorf("get the package branch: %w", err)
	}
	title := fmt.Sprintf("fix(%s): drop what was generated", in.PkgName)
	if err := c.g2.Commit(ctx, g2.HeadBranchName(in.PkgName), parent, title, files); err != nil {
		return fmt.Errorf("commit the removal of the generated files: %w", err)
	}
	pr, err := c.g2.CreatePullRequestFrom(ctx, g2.HeadBranchName(in.PkgName), branch, title,
		dropBody(in, paths, stop))
	if err != nil {
		return fmt.Errorf("open the pull request that drops the generated files: %w", err)
	}
	logger.Info("opened the pull request that drops what was generated",
		"number", pr.GetNumber(), "num_of_files", len(paths))
	return nil
}

// stopBody says what the first pull request does and why it is first.
func stopBody(in *Input) string {
	var b strings.Builder
	b.WriteString("The registry stops serving `" + in.PkgName + "`.\n\n")
	b.WriteString("Why:\n\n> " + strings.ReplaceAll(strings.TrimSpace(in.Reason), "\n", "\n> ") + "\n\n")
	b.WriteString("This is half of it. The package is added to `ignored_packages`, so no run generates " +
		"it again, and its entry goes from `index.json` and `aliases.json`, so nothing searching the " +
		"registry finds it. What it generated is still on its branch, and the pull request that takes " +
		"that away says as much.\n\n")
	b.WriteString("Merge this one first. A package still in the order has its files generated again by " +
		"the next run, whatever the other pull request did.\n\n")
	b.WriteString("Removing a package is not something this registry does as a rule: what it publishes " +
		"for a version is meant to stay what it was, and somebody's configuration may name this package. " +
		"What it can't reach either way is a lock file that already holds it, which carries the URL and " +
		"the checksum of every file it needs.\n")
	return b.String()
}

// dropBody says what the second pull request does and what it doesn't reach.
func dropBody(in *Input, paths []string, stop *gogithub.PullRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Takes the %d generated files of `%s` off its branch, so the registry stops "+
		"answering for the versions it published.\n\n", len(paths), in.PkgName)
	if stop != nil {
		fmt.Fprintf(&b, "The other half is #%d, which stops the package being generated and listed. "+
			"Merge that one first: a package still in the order has these files generated again by the "+
			"next run.\n\n", stop.GetNumber())
	}
	b.WriteString("Why:\n\n> " + strings.ReplaceAll(strings.TrimSpace(in.Reason), "\n", "\n> ") + "\n\n")
	b.WriteString("`aqua lock update` then fails for these versions rather than resolving them, which " +
		"is what stops anybody new installing the package. A lock file that already holds it keeps " +
		"working: it carries the URL and the checksum of every file it needs, and nothing here takes " +
		"that away. Where that matters -- malware -- saying so where people will read it is the part " +
		"that reaches them.\n\n")
	b.WriteString("The branch itself stays. A ruleset forbids deleting a package branch and no app " +
		"bypasses it, so what was published stays readable in its history.\n")
	return b.String()
}
