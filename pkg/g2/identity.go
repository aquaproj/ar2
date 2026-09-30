package g2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"go.yaml.in/yaml/v3"
)

// Identification is what putting a package on the branch named after its id would do.
type Identification struct {
	// Branch is the ref to create.
	Branch string
	// Parent is where the package's branch is now. The new branch starts there, so
	// nothing is copied and the record of how each version arrived is still readable.
	Parent string
	// Config is the definition with the package's own name written into it, or empty
	// when it already says it. A branch named after an id doesn't say which package it
	// holds, so the definition has to.
	Config string
	// Moved is the files whose version directory was written before a version was
	// escaped into one path segment.
	Moved []*MovedFile
}

// MovedFile is a file that belongs at another path.
type MovedFile struct {
	From string
	To   string
	// SHA is the blob, which is written at the new path as it is: the file didn't
	// change, only where it sits.
	SHA string
	// Mode is the file's mode, carried over for the same reason.
	Mode string
}

// PlanIdentity reads what creating the package's id branch would do, and returns nil when
// the branch is already there.
//
// Reading it separately from doing it is what lets the migration be looked at before it
// runs: it walks every package the registry holds, and the branches it creates can't be
// deleted afterwards by anything ar2 has.
func (c *Client) PlanIdentity(ctx context.Context, pkgName, id string) (*Identification, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: %s", errNoIDToIdentify, pkgName)
	}
	branch := BranchPrefix + id
	sha, err := c.BranchSHA(ctx, branch)
	if err != nil {
		return nil, err
	}
	if sha != "" {
		return nil, nil //nolint:nilnil // there is nothing to do, which is not a plan
	}
	parent, err := c.BranchSHA(ctx, BranchName(pkgName))
	if err != nil {
		return nil, err
	}
	if parent == "" {
		return nil, fmt.Errorf("%w: %s", errNoBranchToIdentify, pkgName)
	}

	// The branch named after the package, which is the one being carried over. Asking for
	// the package's branch would answer with the one named after its id, which is what
	// this is about to create.
	cfg, err := c.ConfigOnRef(ctx, BranchName(pkgName))
	if err != nil {
		return nil, err
	}
	config, err := namedConfig(cfg, pkgName)
	if err != nil {
		return nil, err
	}
	moved, err := c.movedVersionFiles(ctx, parent)
	if err != nil {
		return nil, err
	}
	return &Identification{Branch: branch, Parent: parent, Config: config, Moved: moved}, nil
}

// Identify creates the branch named after the package's id.
//
// Created rather than the versions generated again: a package's history is every release
// it published, downloaded and hashed and opened on six machines to get there, and the id
// is the same history addressed by something that doesn't change when the name does.
//
// Created with the app that creates branches, which is what the ruleset requiring status
// checks lets through. Committing onto an existing package branch takes a pull request,
// which is why the corrections are part of the commit the branch starts at.
func (c *Client) Identify(ctx context.Context, pkgName string, plan *Identification) error {
	commit, err := c.identityCommit(ctx, pkgName, plan)
	if err != nil {
		return err
	}
	if _, _, err := c.branchGH.Git.CreateRef(ctx, c.owner, c.repo, gogithub.CreateRef{
		Ref: "refs/heads/" + plan.Branch,
		SHA: commit,
	}); err != nil {
		return fmt.Errorf("create the branch named after the package's id: %w", err)
	}
	return nil
}

// identityCommit is the commit the id branch starts at: the package branch's, with the
// definition naming the package and every version directory at the name it has now.
//
// A package needing neither starts at the commit it is already at, which is most of them
// once they have both.
func (c *Client) identityCommit(ctx context.Context, pkgName string, plan *Identification) (string, error) {
	// A moved file is two entries: written at the path it belongs at and taken out of
	// the one it was at.
	entries := make([]*gogithub.TreeEntry, 0, 1+2*len(plan.Moved))
	if plan.Config != "" {
		entries = append(entries, &gogithub.TreeEntry{
			Path:    new(ConfigFileName),
			Mode:    new(blobMode),
			Type:    new(blobType),
			Content: new(plan.Config),
		})
	}
	for _, moved := range plan.Moved {
		entries = append(entries, &gogithub.TreeEntry{
			Path: new(moved.To),
			Mode: new(moved.Mode),
			Type: new(blobType),
			SHA:  new(moved.SHA),
		})
		entries = append(entries, deletedEntry(moved.From))
	}
	if len(entries) == 0 {
		return plan.Parent, nil
	}

	parentCommit, _, err := c.branchGH.Git.GetCommit(ctx, c.owner, c.repo, plan.Parent)
	if err != nil {
		return "", fmt.Errorf("get the commit the package's branch is at: %w", err)
	}
	tree, _, err := c.branchGH.Git.CreateTree(ctx, c.owner, c.repo,
		parentCommit.GetTree().GetSHA(), entries)
	if err != nil {
		return "", fmt.Errorf("create the tree of the identified package: %w", err)
	}
	commit, _, err := c.branchGH.Git.CreateCommit(ctx, c.owner, c.repo, gogithub.Commit{
		Message: new("chore: hold " + pkgName + " on the branch named after its id"),
		Tree:    tree,
		Parents: []*gogithub.Commit{{SHA: new(plan.Parent)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create the commit of the identified package: %w", err)
	}
	return commit.GetSHA(), nil
}

// namedConfig is the definition with the package's own name in it, or empty when it
// already names it.
//
// The branch name is what said which package a branch holds. An id says nothing, so the
// definition is where the branch answers for itself, and reaching a branch is how a name
// is checked against what was asked for.
func namedConfig(cfg *aquag2.Config, pkgName string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("%w: %s", errNoConfigToIdentify, pkgName)
	}
	if cfg.Name == pkgName {
		return "", nil
	}
	cfg.Name = pkgName
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal the definition naming the package: %w", err)
	}
	return string(b), nil
}

// movedVersionFiles reads the files on the branch whose version directory was written
// before a version was escaped into one path segment.
func (c *Client) movedVersionFiles(ctx context.Context, commit string) ([]*MovedFile, error) {
	tree, resp, err := c.gh.Git.GetTree(ctx, c.owner, c.repo, commit+":"+aquag2.VersionDir, true)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			// A branch carrying nothing but a definition, which has no version to
			// put anywhere.
			return nil, nil
		}
		return nil, fmt.Errorf("get the versions directory of the package's branch: %w", err)
	}
	if tree.GetTruncated() {
		return nil, errVersionsTruncated
	}
	var moved []*MovedFile
	for _, entry := range tree.Entries {
		if entry.GetType() != blobType {
			continue
		}
		// The tree was addressed at versions/, so the paths come back relative to it.
		from := aquag2.VersionDir + "/" + entry.GetPath()
		to, ok := escapedVersionPath(from)
		if !ok {
			continue
		}
		moved = append(moved, &MovedFile{
			From: from,
			To:   to,
			SHA:  entry.GetSHA(),
			Mode: entry.GetMode(),
		})
	}
	return moved, nil
}

// escapedVersionPath is where a file under versions/ belongs once its version is one path
// segment, and false when it is already there.
//
// The directory a file is in is the version. A name that decodes is one the escaping
// produced, so it is left alone; anything else is the version written as it is, which for
// "kustomize/v5.8.1" was two directories rather than one.
func escapedVersionPath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, aquag2.VersionDir+"/")
	if !ok {
		return "", false
	}
	dir, file, ok := strings.CutLast(rest, "/")
	if !ok {
		// A file sitting directly under versions/, which is no version's.
		return "", false
	}
	if _, ok := aquag2.DecodeVersion(dir); ok {
		return "", false
	}
	return aquag2.VersionDir + "/" + aquag2.EncodeVersion(dir) + "/" + file, true
}

var (
	// errNoIDToIdentify says the catalogue doesn't give the package an id, which is
	// what its branch would be named after.
	errNoIDToIdentify = errors.New("the catalogue gives the package no id")
	// errNoBranchToIdentify says the registry holds no branch for the package, so
	// there is no history for the id branch to start at.
	errNoBranchToIdentify = errors.New("the package has no branch to identify")
	// errNoConfigToIdentify says the package's branch holds no definition, so there is
	// nothing to write the package's own name into.
	errNoConfigToIdentify = errors.New("the package's branch has no definition")
)
