package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/google/go-github/v92/github"
	"gopkg.in/yaml.v3"
)

// ScaffoldPath returns the Contents API path of a package's scaffold.yaml at ref.
func ScaffoldPath(ref, pkgName string) string {
	return fmt.Sprintf("repos/%s/%s/contents/pkgs/%s/scaffold.yaml?ref=%s", Owner, Name, pkgName, url.QueryEscape(ref))
}

// FetchScaffold returns a package's aqua gr configuration, or nil when it has none.
//
// Most packages have none: the file exists only where the release needs something the
// inference can't work out, such as which of its assets is the command. A package
// without one is the normal case rather than a failure, so a missing file reads as
// nothing to apply.
func FetchScaffold(ctx context.Context, client Client, ref, pkgName string) (*genrgst.RawConfig, error) {
	req, err := client.NewRequest(ctx, http.MethodGet, ScaffoldPath(ref, pkgName), nil)
	if err != nil {
		return nil, fmt.Errorf("create a request for scaffold.yaml: %w", err)
	}
	req.Header.Set("Accept", mediaTypeRaw)
	buf := &bytes.Buffer{}
	if _, err := client.Do(req, buf); err != nil {
		var errRes *github.ErrorResponse
		if errors.As(err, &errRes) && errRes.Response != nil && errRes.Response.StatusCode == http.StatusNotFound {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("get scaffold.yaml: %w", err)
	}
	cfg := &genrgst.RawConfig{}
	if err := yaml.NewDecoder(buf).Decode(cfg); err != nil {
		return nil, fmt.Errorf("read scaffold.yaml as YAML: %w", err)
	}
	return cfg, nil
}
