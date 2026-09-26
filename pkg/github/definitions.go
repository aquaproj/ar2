package github

import (
	"context"
	"fmt"
	"log/slog"
)

// refsPerPage is how many branches one request asks about.
//
// A hundred is GraphQL's limit for a connection. The whole registry is sixteen requests
// at that size, and GitHub charges each of them about a point: the cost of reading every
// definition this way is a rounding error against a budget of five thousand an hour,
// where reading them one at a time is a request per package.
const refsPerPage = 100

// Branches reads files on one repository's branches.
type Branches struct {
	c     *Client
	owner string
	repo  string
}

// Branches returns a reader of the repository's branches.
func (c *Client) Branches(owner, repo string) *Branches {
	return &Branches{c: c, owner: owner, repo: repo}
}

// Files returns the file at path on every branch whose name starts with prefix and which
// has one, keyed by branch name.
//
// One query per hundred branches rather than a request per branch. What this answers is
// what every package's definition says right now, which is the only thing that can be
// compared against what the catalogue says about it: an entry is made once, out of the
// definition the package arrived with, and a definition edited afterwards leaves the entry
// describing the package as it was. Nothing else notices, because the question "which
// packages is the catalogue missing" isn't the question.
//
// A branch whose file isn't there is left out. GitHub reports that as an error against
// that one branch and answers for the others in the same response, so it is read as the
// answer it is: that is what a package branch looks like before its first pull request
// merges, and there is nothing to describe the package with until then.
func (b *Branches) Files(ctx context.Context, logger *slog.Logger, prefix, path string) (map[string]string, error) {
	files := map[string]string{}
	cursor := ""
	for {
		page, err := b.page(ctx, prefix, path, cursor)
		if err != nil {
			return nil, err
		}
		refs := page.Data.Repository.Refs
		for _, node := range refs.Nodes {
			text, ok := blobText(logger, node, path)
			if !ok {
				continue
			}
			files[node.Name] = text
		}
		if !refs.PageInfo.HasNextPage {
			return files, nil
		}
		cursor = refs.PageInfo.EndCursor
	}
}

// blobText reads the file out of one branch's answer.
func blobText(logger *slog.Logger, node refNode, path string) (string, bool) {
	blob := node.Target.File
	if blob == nil || blob.Object == nil {
		return "", false
	}
	if blob.Object.IsTruncated {
		// A file GitHub wouldn't send whole would parse as something the branch
		// doesn't say. Left out, so the entry made from it is the one that is there
		// rather than one made from half a file.
		logger.Warn("a branch holds a file too large to read in one answer",
			"branch", node.Name, "path", path)
		return "", false
	}
	return blob.Object.Text, true
}

// page asks for one page of branches.
func (b *Branches) page(ctx context.Context, prefix, path, cursor string) (*branchesResponse, error) {
	vars := map[string]any{
		"owner":  b.owner,
		"name":   b.repo,
		"path":   path,
		"prefix": prefix,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}
	result := &branchesResponse{}
	if err := b.c.post(ctx, branchesQuery(), vars, result); err != nil {
		return nil, err
	}
	if err := branchesError(result.Errors); err != nil {
		return nil, err
	}
	return result, nil
}

// branchesError is whichever of the errors means the answer can't be used.
//
// A branch that doesn't have the file is NOT_FOUND against that branch alone, and the rest
// of the page is in the same response: reading that as a failure would throw away every
// other branch's definition over a package waiting for its first pull request. Anything
// else -- a rate limit, a query GitHub won't run -- is a page that didn't answer, and a
// page silently missing would look exactly like a catalogue in step with it.
func branchesError(errs []graphQLError) error {
	for _, e := range errs {
		if e.Type == errNotFound {
			continue
		}
		return fmt.Errorf("read the branches: %s", e.Message)
	}
	return nil
}

// errNotFound is the error type GitHub returns for a file a branch doesn't have.
const errNotFound = "NOT_FOUND"

// branchesQuery asks each branch for one file.
//
// Ordered by name so that paging through them is stable: a branch created while the pages
// are being read would otherwise be able to shift the ones that follow. The prefix is
// GitHub's own filter rather than one applied to the answer, so the branches a pull
// request opens don't take up the pages.
func branchesQuery() string {
	return fmt.Sprintf(`query($owner: String!, $name: String!, $path: String!, $prefix: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    refs(refPrefix: "refs/heads/", query: $prefix, first: %d, after: $cursor, orderBy: {field: ALPHABETICAL, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        name
        target {
          ... on Commit {
            file(path: $path) {
              object { ... on Blob { text isTruncated } }
            }
          }
        }
      }
    }
  }
}`, refsPerPage)
}

type branchesResponse struct {
	Data struct {
		Repository struct {
			Refs struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []refNode `json:"nodes"`
			} `json:"refs"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type refNode struct {
	Name   string `json:"name"`
	Target struct {
		File *struct {
			Object *struct {
				Text        string `json:"text"`
				IsTruncated bool   `json:"isTruncated"`
			} `json:"object"`
		} `json:"file"`
	} `json:"target"`
}
