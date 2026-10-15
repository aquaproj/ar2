package run

import (
	"errors"
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/verify"
)

func provenanced() *generate.Generated {
	asset := "aidlc-release.intoto.jsonl"
	return &generate.Generated{
		Registry: &generate.Registry{Assets: []*generate.Asset{{
			OS: "darwin", Arch: "amd64", Asset: "aidlc-darwin-x64",
			SLSAProvenance: &aquaregistry.SLSAProvenance{Type: "github_release", Asset: &asset},
		}}},
		Inferred: map[string]bool{generate.KindSLSAProvenance: true},
	}
}

// A signature read off the release that doesn't verify is taken off, and the entries are
// completed again without it.
func TestWithoutFailedGuesses_inferred(t *testing.T) {
	t.Parallel()
	generated := provenanced()
	calls := 0
	_, err := withoutFailedGuesses(slog.New(slog.DiscardHandler), "awslabs/aidlc-workflows", "v2.11.0", generated, func() (*verify.Filled, error) {
		calls++
		if generated.Registry.Assets[0].SLSAProvenance != nil {
			return nil, &sign.UnverifiedError{Kind: generate.KindSLSAProvenance, Err: errors.New("verify with slsa-verifier")}
		}
		return &verify.Filled{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("completed %d times, want twice", calls)
	}
}

// A signature the definition names that doesn't verify is what it was: the release isn't
// what it says.
func TestWithoutFailedGuesses_declared(t *testing.T) {
	t.Parallel()
	generated := provenanced()
	generated.Inferred = map[string]bool{}
	_, err := withoutFailedGuesses(slog.New(slog.DiscardHandler), "awslabs/aidlc-workflows", "v2.11.0", generated, func() (*verify.Filled, error) {
		return nil, &sign.UnverifiedError{Kind: generate.KindSLSAProvenance, Err: errors.New("verify with slsa-verifier")}
	})
	if !verify.Tampered(err) {
		t.Errorf("got %v, want the version stopped as tampered with", err)
	}
	if generated.Registry.Assets[0].SLSAProvenance == nil {
		t.Error("the provenance the definition names was taken off")
	}
}
