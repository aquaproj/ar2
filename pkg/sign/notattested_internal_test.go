package sign

import (
	"errors"
	"testing"
)

// GitHub holding no attestation is told apart from one that didn't verify: only the first
// can come off an entry being generated.
func TestNotAttested(t *testing.T) {
	t.Parallel()
	for msg, want := range map[string]bool{
		// What gh said for FiloSottile/age v1.2.1, released before it was attested.
		"verify the attestation: exit status 1: Error: HTTP 404: Not Found (https://api.github.com/orgs/FiloSottile/attestations/sha256:424e1d?per_page=30)": true,
		"verify the attestation: exit status 1: Error: no attestations found for subject":                                                                    true,
		"verify the attestation: exit status 1: Error: expected SourceRepositoryDigest to be d528f20f, got a255baf7":                                         false,
		"verify the attestation: exit status 1: Error: HTTP 404: Not Found (https://api.github.com/repos/x/y)":                                               false,
		"verify the attestation: exit status 4: Error: authentication required":                                                                              false,
	} {
		if got := notAttested(errors.New(msg)); got != want {
			t.Errorf("notAttested(%q) = %v, want %v", msg, got, want)
		}
	}
}
