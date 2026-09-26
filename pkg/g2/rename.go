package g2

import (
	"context"
	"errors"
	"fmt"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// RenamePackage puts a package's branch under its new name, carrying everything on it.
//
// The branch is created rather than the versions generated again. A package's history is
// every release it ever published, downloaded and hashed and opened on six machines to
// get there; the new name is the same history under another word for it. The old commit
// becomes the new branch's parent, so nothing is copied and the record of how each
// version arrived is still readable.
//
// Creating a branch is what this can do. Committing onto one takes a pull request -- the
// ruleset says so and no app bypasses it -- so the corrected definition is part of the
// commit the branch starts at rather than a change made afterwards.
//
// It returns false when there was nothing to move: no branch under the old name, or one
// already under the new one. Both are what a second run of the same rename sees.
func (c *Client) RenamePackage(ctx context.Context, from, to string) (bool, error) {
	oldBranch, newBranch := BranchName(from), BranchName(to)
	parent, err := c.BranchSHA(ctx, oldBranch)
	if err != nil {
		return false, err
	}
	if parent == "" {
		return false, fmt.Errorf("%w: %s", errNoBranchToRename, from)
	}
	if sha, err := c.BranchSHA(ctx, newBranch); err != nil {
		return false, err
	} else if sha != "" {
		return false, nil
	}

	cfg, err := c.Config(ctx, from)
	if err != nil {
		return false, err
	}
	content, err := renamedConfig(cfg, from, to)
	if err != nil {
		return false, err
	}

	commit, err := c.renameCommit(ctx, parent, from, to, content)
	if err != nil {
		return false, err
	}
	if _, _, err := c.branchGH.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
		Ref: "refs/heads/" + newBranch,
		SHA: commit,
	}); err != nil {
		return false, fmt.Errorf("create the branch of the renamed package: %w", err)
	}
	return true, nil
}

// renameCommit is the commit the new branch starts at: the old branch's, with the
// definition corrected.
//
// Built with the app that creates branches rather than the one that opens pull requests,
// because this commit is only ever reached by a branch being created at it.
func (c *Client) renameCommit(ctx context.Context, parent, from, to, config string) (string, error) {
	parentCommit, _, err := c.branchGH.Git.GetCommit(ctx, c.owner, c.repo, parent)
	if err != nil {
		return "", fmt.Errorf("get the commit of the branch being renamed: %w", err)
	}
	tree, _, err := c.branchGH.Git.CreateTree(ctx, c.owner, c.repo, parentCommit.GetTree().GetSHA(),
		[]*gogithub.TreeEntry{{
			Path:    new(ConfigFileName),
			Mode:    new(blobMode),
			Type:    new(blobType),
			Content: new(config),
		}})
	if err != nil {
		return "", fmt.Errorf("create the tree of the renamed package: %w", err)
	}
	commit, _, err := c.branchGH.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
		Message: new("chore: rename " + from + " to " + to),
		Tree:    tree,
		Parents: []*gogithub.Commit{{SHA: new(parent)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create the commit of the renamed package: %w", err)
	}
	return commit.GetSHA(), nil
}

// renamedConfig is the definition under the new name.
//
// The old name becomes an alias, kept alongside the ones it already had rather than
// replacing them: a package renamed twice has to answer to both of its old names, and a
// definition recording only the last one would leave the older pointing at a name
// nothing holds any more.
func renamedConfig(cfg *aquag2.Config, from, to string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("%w: %s", errNoConfigToRename, from)
	}
	owner, name, err := splitRepo(to)
	if err != nil {
		return "", err
	}
	cfg.RepoOwner, cfg.RepoName = owner, name
	if cfg.Name != "" {
		cfg.Name = to
	}
	if !hasAlias(cfg, from) {
		cfg.Aliases = append(cfg.Aliases, &aquaregistry.Alias{Name: from})
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal the definition of the renamed package: %w", err)
	}
	return string(b), nil
}

// splitRepo reads the owner and the repository out of a package name.
//
// A package name is usually the repository it comes from, and sometimes a command inside
// one: kubernetes/kubernetes/kubectl is the kubectl of kubernetes/kubernetes. The first
// two parts are the repository either way.
func splitRepo(pkgName string) (string, string, error) {
	parts := strings.SplitN(pkgName, "/", 3) //nolint:mnd
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: %s", errNotARepoName, pkgName)
	}
	return parts[0], parts[1], nil
}

// hasAlias reports whether the definition already names it.
func hasAlias(cfg *aquag2.Config, name string) bool {
	for _, alias := range cfg.Aliases {
		if alias != nil && alias.Name == name {
			return true
		}
	}
	return false
}

// ErrNoBranchToRename says the rename failed because the registry doesn't hold the
// package.
//
// Which is not a failure of the rename so much as a rename with nothing to do: what the
// caller has to move is the package's place in its own records, and the registry only ever
// sees the new name.
func ErrNoBranchToRename(err error) bool {
	return errors.Is(err, errNoBranchToRename)
}

var (
	// errNoBranchToRename is what a rename of a package the registry doesn't hold gets.
	// There is nothing to carry over, and generating it under the new name is what the
	// ordinary run does.
	errNoBranchToRename = errors.New("the package has no branch to rename")
	// errNoConfigToRename is what a branch with no definition gets. Its versions were
	// never generated, so the new name has nothing to inherit either.
	errNoConfigToRename = errors.New("the package's branch has no definition")
	// errNotARepoName says a package name doesn't name a repository, which is what the
	// renamed definition has to record.
	errNotARepoName = errors.New("the package name doesn't name a repository")
)
