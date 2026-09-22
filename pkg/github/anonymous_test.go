package github_test

import (
	"testing"

	"github.com/aquaproj/ar2/pkg/github"
)

// TestFillForbiddenStars checks that a repository an organization's IP allow list
// refused is read anonymously, and that one which doesn't exist is left alone.
//
// The allow list covers public repositories whenever the request is authenticated,
// and the GitHub Actions token counts, so a run in Actions loses the star count of
// every package such an organization owns. Anonymous requests aren't covered.
func TestFillForbiddenStars(t *testing.T) {
	t.Parallel()
	stars := map[string]int{}
	reasons := map[string]string{
		// Owned by an organization with an IP allow list.
		"Shopify/ejson": "FORBIDDEN",
		// Really gone; asking again only spends the anonymous rate limit.
		"xtaci/kcptun": "NOT_FOUND",
	}
	github.NewClient(nil).FillForbiddenStars(t.Context(), stars, reasons)

	if _, ok := stars["Shopify/ejson"]; !ok {
		t.Error("a refused repository should have been read anonymously")
	}
	if _, ok := reasons["Shopify/ejson"]; ok {
		t.Error("a repository that was read should no longer be a failure")
	}
	if reasons["xtaci/kcptun"] != "NOT_FOUND" {
		t.Error("a repository that doesn't exist should be left alone")
	}
	if _, ok := stars["xtaci/kcptun"]; ok {
		t.Error("a repository that doesn't exist has no star count")
	}
}
