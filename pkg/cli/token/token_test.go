package token_test

import (
	"log/slog"
	"testing"

	"github.com/aquaproj/ar2/pkg/cli/token"
)

// GitHub's API is asked with AR2_API_TOKEN when it is set, and with GITHUB_TOKEN otherwise.
func TestAPI(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	t.Setenv("GITHUB_TOKEN", "actions")

	t.Setenv(token.APIEnv, "app")
	if got := tokenOf(t, logger); got != "app" {
		t.Errorf("got %q, want the app's token", got)
	}

	t.Setenv(token.APIEnv, "")
	if got := tokenOf(t, logger); got != "actions" {
		t.Errorf("got %q, want GITHUB_TOKEN", got)
	}
}

func tokenOf(t *testing.T, logger *slog.Logger) string {
	t.Helper()
	ts, err := token.API(logger)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	return tok.AccessToken
}
