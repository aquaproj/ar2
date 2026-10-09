package verify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/generate"
)

func TestFetches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		typ  string
		want bool
	}{
		{typ: aquaregistry.PkgInfoTypeGitHubRelease, want: true},
		{typ: aquaregistry.PkgInfoTypeHTTP, want: true},
		// These build from source through another tool that verifies for itself, so
		// there is no artifact to hash.
		{typ: aquaregistry.PkgInfoTypeGoInstall, want: false},
		{typ: aquaregistry.PkgInfoTypeCargo, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			t.Parallel()
			if got := fetches(&generate.Asset{Type: tt.typ}); got != tt.want {
				t.Errorf("fetches(%q) is %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

func TestSetChecksum(t *testing.T) {
	t.Parallel()
	t.Run("records a missing checksum", func(t *testing.T) {
		t.Parallel()
		a := &generate.Asset{}
		if err := setChecksum(a, "abc"); err != nil {
			t.Fatal(err)
		}
		if a.Checksum != "abc" || a.ChecksumAlgorithm != ChecksumAlgorithm {
			t.Errorf("the checksum wasn't recorded: %+v", a)
		}
	})
	t.Run("accepts a digest that matches", func(t *testing.T) {
		t.Parallel()
		if err := setChecksum(&generate.Asset{Checksum: "abc"}, "abc"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects a digest that doesn't", func(t *testing.T) {
		t.Parallel()
		// Disagreeing means the asset was replaced after it was published, which is
		// the case a checksum exists to catch.
		if err := setChecksum(&generate.Asset{Checksum: "abc", Asset: "a.tar.gz"}, "def"); err == nil {
			t.Error("a digest that doesn't match the bytes must be an error")
		}
	})
}

// A cargo entry has no artifact, so filling it asks nothing of the release and leaves
// nothing for a person to look at. Before, every version of a cargo package went out for
// review over a check that could never have run.
func TestFill_buildsRatherThanFetches(t *testing.T) {
	t.Parallel()
	reg := &generate.Registry{Assets: []*generate.Asset{
		{OS: "linux", Arch: "amd64", Type: aquaregistry.PkgInfoTypeCargo, Crate: "bat"},
		{OS: "darwin", Arch: "arm64", Type: aquaregistry.PkgInfoTypeGoInstall},
	}}
	needsReview, unresolved, err := New(nil, nil).Fill(t.Context(), discardLogger(), "crates.io/bat", "0.25.0", reg, true)
	if err != nil {
		t.Fatal(err)
	}
	if needsReview {
		t.Error("an entry with no artifact has nothing to review")
	}
	if len(unresolved) != 0 {
		t.Errorf("an entry with no artifact has no files to resolve, got %v", unresolved)
	}
	for _, a := range reg.Assets {
		if a.Checksum != "" {
			t.Errorf("%s/%s carries a checksum of nothing", a.OS, a.Arch)
		}
	}
}

// A 404 is the release saying it has no such asset, which is about the release and not
// about the moment: the entry goes, and the environments that do work keep the version.
func TestRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "not there", err: fmt.Errorf("download: %w", &StatusError{Code: 404}), want: true},
		{name: "not ours", err: &StatusError{Code: 403}, want: true},
		{name: "a bad minute", err: &StatusError{Code: 500}, want: false},
		{name: "a rate limit is the moment", err: &StatusError{Code: 429}, want: false},
		{name: "not a status at all", err: errors.New("connection reset by peer"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Refused(tt.err); got != tt.want {
				t.Errorf("Refused is %v, want %v", got, tt.want)
			}
		})
	}
}

// An environment the release publishes nothing for is left out, and the ones it does
// publish keep the version.
func TestComplete_leavesTheEnvironmentOut(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "windows") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "an asset")
	}))
	defer srv.Close()

	reg := &generate.Registry{Assets: []*generate.Asset{
		{OS: "linux", Arch: "amd64", Format: "raw", URL: srv.URL + "/linux-amd64"},
		{OS: "windows", Arch: "amd64", Format: "raw", URL: srv.URL + "/windows-amd64"},
	}}
	v := New(srv.Client(), nil)
	filled, err := v.Complete(context.Background(), discardLogger(), "a/package", "v1.0.0", reg, false)
	if err != nil {
		t.Fatal(err)
	}
	if filled.Offered != 1 {
		t.Errorf("the file offers %d environments", filled.Offered)
	}
	if len(reg.Assets) != 1 || reg.Assets[0].OS != "linux" {
		t.Fatalf("what the file holds is %+v", reg.Assets)
	}
	if len(filled.Excluded) != 1 {
		t.Fatalf("what went is %+v", filled.Excluded)
	}
	if got := filled.Excluded[0].Environment(); got != "windows/amd64" {
		t.Errorf("what went is %q", got)
	}
	if !strings.Contains(filled.Excluded[0].Reason, "404") {
		t.Errorf("why it went is %q", filled.Excluded[0].Reason)
	}
}

// A version the release publishes nothing for at all is not a file: the entries stay as
// they were generated, and what becomes of it is the caller's.
func TestComplete_nothingIsLeft(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	reg := &generate.Registry{Assets: []*generate.Asset{
		{OS: "linux", Arch: "amd64", Format: "raw", URL: srv.URL + "/linux-amd64"},
	}}
	v := New(srv.Client(), nil)
	filled, err := v.Complete(context.Background(), discardLogger(), "a/package", "v1.0.0", reg, false)
	if err != nil {
		t.Fatal(err)
	}
	if filled.Offered != 0 {
		t.Errorf("the file offers %d environments", filled.Offered)
	}
	if len(reg.Assets) != 1 {
		t.Errorf("what the file holds is %+v", reg.Assets)
	}
}
