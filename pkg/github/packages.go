package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// blobsPerQuery is how many files one request reads.
//
// Each is an alias of its own in one query, and GitHub charges a query by what it asks for
// rather than by how many aliases ask it, so a hundred cost about what one does. The whole
// registry is a few dozen requests at this size, where a request per package would be
// thousands.
const blobsPerQuery = 100

// Repository reads the files and trees of one repository.
type Repository struct {
	c     *Client
	owner string
	repo  string
}

// Repository returns a reader of the repository.
func (c *Client) Repository(owner, repo string) *Repository {
	return &Repository{c: c, owner: owner, repo: repo}
}

// Subtrees returns the directories two levels under the tree an expression names, such as
// "69/1790772769" under "main:pkgs".
//
// One request for every package the registry holds: the two levels are names and nothing
// else, a few dozen bytes a package. Reading the tree recursively instead would list every
// version of every package, and GitHub stops a recursive listing at 100,000 entries.
//
// A tree the expression doesn't name holds nothing, which is what a registry no package
// has been moved into yet looks like.
func (r *Repository) Subtrees(ctx context.Context, expression string) ([]string, error) {
	result := &subtreesResponse{}
	vars := r.vars()
	vars["expression"] = expression
	if err := r.c.post(ctx, subtreesQuery, vars, result); err != nil {
		return nil, err
	}
	if err := firstError(result.Errors); err != nil {
		return nil, fmt.Errorf("read the tree %s: %w", expression, err)
	}
	object := result.Data.Repository.Object
	if object == nil {
		return nil, nil
	}
	out := []string{}
	for _, dir := range object.Entries {
		if dir.Type != treeType || dir.Object == nil {
			continue
		}
		for _, sub := range dir.Object.Entries {
			if sub.Type != treeType {
				continue
			}
			out = append(out, dir.Name+"/"+sub.Name)
		}
	}
	return out, nil
}

const treeType = "tree"

const subtreesQuery = `query($owner: String!, $name: String!, $expression: String!) {
  repository(owner: $owner, name: $name) {
    object(expression: $expression) {
      ... on Tree {
        entries {
          name
          type
          object { ... on Tree { entries { name type } } }
        }
      }
    }
  }
}`

type subtreesResponse struct {
	Data struct {
		Repository struct {
			Object *struct {
				Entries []struct {
					Name   string `json:"name"`
					Type   string `json:"type"`
					Object *struct {
						Entries []struct {
							Name string `json:"name"`
							Type string `json:"type"`
						} `json:"entries"`
					} `json:"object"`
				} `json:"entries"`
			} `json:"object"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// Blobs returns the text of the file each expression names, such as
// "main:pkgs/69/1790772769/registry.yaml", keyed by the expression.
//
// An expression naming nothing is left out rather than failing the rest: that is what the
// head branch of a pull request closed since it was listed looks like, and the files the
// other expressions name are no less there for it.
//
// A file GitHub wouldn't send whole is left out too, and said so. It would parse as
// something the file doesn't say.
func (r *Repository) Blobs(ctx context.Context, logger *slog.Logger, expressions []string) (map[string]string, error) {
	out := make(map[string]string, len(expressions))
	for start := 0; start < len(expressions); start += blobsPerQuery {
		batch := expressions[start:min(start+blobsPerQuery, len(expressions))]
		result := &blobsResponse{}
		if err := r.c.post(ctx, blobsQuery(batch), r.vars(), result); err != nil {
			return nil, err
		}
		if err := firstError(result.Errors); err != nil {
			return nil, fmt.Errorf("read files: %w", err)
		}
		for i, expression := range batch {
			blob := result.Data.Repository[blobAlias(i)]
			if blob == nil {
				continue
			}
			if blob.IsTruncated {
				logger.Warn("a file is too large to read in one answer", "expression", expression)
				continue
			}
			out[expression] = blob.Text
		}
	}
	return out, nil
}

func blobAlias(i int) string {
	return "f" + strconv.Itoa(i)
}

// blobsQuery asks for each expression under an alias of its own. The expressions are
// written into the query as string literals, quoted the way JSON quotes them, which is the
// way GraphQL does.
func blobsQuery(expressions []string) string {
	var b strings.Builder
	b.WriteString("query($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
	for i, expression := range expressions {
		literal, _ := json.Marshal(expression) //nolint:errchkjson // a string always marshals
		fmt.Fprintf(&b, "    %s: object(expression: %s) { ... on Blob { text isTruncated } }\n", blobAlias(i), literal)
	}
	b.WriteString("  }\n}")
	return b.String()
}

type blobsResponse struct {
	Data struct {
		Repository map[string]*struct {
			Text        string `json:"text"`
			IsTruncated bool   `json:"isTruncated"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// OpenPullRequestHeads returns the head branch of every open pull request.
func (r *Repository) OpenPullRequestHeads(ctx context.Context) ([]string, error) {
	out := []string{}
	cursor := ""
	for {
		vars := r.vars()
		if cursor != "" {
			vars["cursor"] = cursor
		}
		result := &pullRequestsResponse{}
		if err := r.c.post(ctx, pullRequestsQuery, vars, result); err != nil {
			return nil, err
		}
		if err := firstError(result.Errors); err != nil {
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		prs := result.Data.Repository.PullRequests
		for _, node := range prs.Nodes {
			out = append(out, node.HeadRefName)
		}
		if !prs.PageInfo.HasNextPage {
			return out, nil
		}
		cursor = prs.PageInfo.EndCursor
	}
}

const pullRequestsQuery = `query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: OPEN, first: 100, after: $cursor) {
      pageInfo { hasNextPage endCursor }
      nodes { headRefName }
    }
  }
}`

type pullRequestsResponse struct {
	Data struct {
		Repository struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					HeadRefName string `json:"headRefName"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// firstError is the first error of an answer, which means the answer can't be used: a rate
// limit, or a query GitHub won't run. An expression naming nothing isn't one -- GitHub
// answers it with a null object and no error.
func firstError(errs []graphQLError) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", errs[0].Message) //nolint:err113 // GitHub's own message
}

// FilesTwoDeep returns, for each expression naming a tree, the files two levels under it,
// such as "v1.0.0/registry-1.json" under "main:pkgs/67/1790772767/versions". An expression
// naming nothing has no entry.
func (r *Repository) FilesTwoDeep(ctx context.Context, expressions []string) (map[string][]string, error) {
	out := make(map[string][]string, len(expressions))
	for start := 0; start < len(expressions); start += treesPerQuery {
		batch := expressions[start:min(start+treesPerQuery, len(expressions))]
		result := &treesResponse{}
		if err := r.c.post(ctx, objectsQuery(batch, treeSelection), r.vars(), result); err != nil {
			return nil, err
		}
		if err := firstError(result.Errors); err != nil {
			return nil, fmt.Errorf("read trees: %w", err)
		}
		for i, expression := range batch {
			tree := result.Data.Repository[blobAlias(i)]
			if tree == nil {
				continue
			}
			out[expression] = tree.files()
		}
	}
	return out, nil
}

// OIDs returns the sha of the object each expression names, keyed by the expression.
func (r *Repository) OIDs(ctx context.Context, expressions []string) (map[string]string, error) {
	out := make(map[string]string, len(expressions))
	for start := 0; start < len(expressions); start += blobsPerQuery {
		batch := expressions[start:min(start+blobsPerQuery, len(expressions))]
		result := &oidsResponse{}
		if err := r.c.post(ctx, objectsQuery(batch, "oid"), r.vars(), result); err != nil {
			return nil, err
		}
		if err := firstError(result.Errors); err != nil {
			return nil, fmt.Errorf("read objects: %w", err)
		}
		for i, expression := range batch {
			if o := result.Data.Repository[blobAlias(i)]; o != nil {
				out[expression] = o.OID
			}
		}
	}
	return out, nil
}

// treesPerQuery is how many trees one request lists. A package's versions directory is a
// directory per version, so a hundred of them is tens of thousands of entries, which GitHub
// takes long enough over to time out.
const treesPerQuery = 10

const treeSelection = "... on Tree { entries { name type object { ... on Tree { entries { name type } } } } }"

// objectsQuery asks for each expression under an alias of its own, with the same selection.
func objectsQuery(expressions []string, selection string) string {
	var b strings.Builder
	b.WriteString("query($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
	for i, expression := range expressions {
		literal, _ := json.Marshal(expression) //nolint:errchkjson // a string always marshals
		fmt.Fprintf(&b, "    %s: object(expression: %s) { %s }\n", blobAlias(i), literal, selection)
	}
	b.WriteString("  }\n}")
	return b.String()
}

type treesResponse struct {
	Data struct {
		Repository map[string]*twoDeepTree `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type twoDeepTree struct {
	Entries []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Object *struct {
			Entries []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"entries"`
		} `json:"object"`
	} `json:"entries"`
}

// files is the files two levels down, as dir/file.
func (t *twoDeepTree) files() []string {
	files := []string{}
	for _, dir := range t.Entries {
		if dir.Type != treeType || dir.Object == nil {
			continue
		}
		for _, f := range dir.Object.Entries {
			if f.Type == "blob" {
				files = append(files, dir.Name+"/"+f.Name)
			}
		}
	}
	return files
}

type oidsResponse struct {
	Data struct {
		Repository map[string]*struct {
			OID string `json:"oid"`
		} `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// vars is what every query is asked about: the repository.
func (r *Repository) vars() map[string]any {
	return map[string]any{"owner": r.owner, "name": r.repo}
}
