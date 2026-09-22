package sign

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// A workflow signs under the ref it ran for, so its name holds the version. Pinning
// it as it stands would only ever match the one release.
func TestIdentity_template(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		san     string
		version string
		want    string
	}{
		{
			name:    "a workflow signing for a tag",
			san:     "https://github.com/goreleaser/goreleaser/.github/workflows/release.yml@refs/tags/v2.18.2",
			version: "v2.18.2",
			want:    "https://github.com/goreleaser/goreleaser/.github/workflows/release.yml@refs/tags/{{.Version}}",
		},
		{
			// sigstore/cosign signs with a service account, whose name doesn't move.
			name:    "a service account",
			san:     "keyless@projectsigstore.iam.gserviceaccount.com",
			version: "v3.1.3",
			want:    "keyless@projectsigstore.iam.gserviceaccount.com",
		},
		{
			// A workflow that signs from a branch rather than the tag names no
			// version either.
			name:    "a workflow signing from a branch",
			san:     "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
			version: "v2.82.1",
			want:    "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (&Identity{SAN: tt.san, Issuer: "issuer"}).template(tt.version)
			if got.SAN != tt.want {
				t.Errorf("the pinned identity is %q, want %q", got.SAN, tt.want)
			}
			if got.Issuer != "issuer" {
				t.Errorf("the issuer changed to %q", got.Issuer)
			}
		})
	}
}

// The guessed pattern goes and the name takes its place; everything else cosign was
// going to be told stays.
func TestReplaceIdentity(t *testing.T) {
	t.Parallel()
	opts := []string{
		"--certificate", "https://example.com/cert.pem",
		flagIdentityRegexp, `^https://github\.com/cli/.+$`,
		flagIssuer, "https://token.actions.githubusercontent.com",
	}
	want := []string{
		"--certificate", "https://example.com/cert.pem",
		flagIdentity, "someone",
		flagIssuer, "an issuer",
	}
	got := replaceIdentity(opts, &Identity{SAN: "someone", Issuer: "an issuer"})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the options are wrong (-want +got):\n%s", diff)
	}
}

func TestIdentityOpt(t *testing.T) {
	t.Parallel()
	opts := []string{flagIdentity, "someone", flagIssuer, "an issuer"}
	if got := identityOpt(opts, flagIdentity); got != "someone" {
		t.Errorf("got %q, want someone", got)
	}
	if got := identityOpt(opts, flagIdentityRegexp); got != "" {
		t.Errorf("a flag that isn't there returned %q", got)
	}
	// A flag with nothing after it is not a value.
	if got := identityOpt([]string{flagIdentity}, flagIdentity); got != "" {
		t.Errorf("a flag with no value returned %q", got)
	}
}
