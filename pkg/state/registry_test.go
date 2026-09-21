package state_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/szksh-lab-2/ar2/pkg/state"
)

func TestFlags_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		flags   *state.Flags
		env     string
		want    *state.Registry
		wantErr bool
	}{
		{
			name:  "all flags given",
			flags: &state.Flags{Registry: "ghcr.io", Repository: "aquaproj/ar2", Username: "someone"},
			want:  &state.Registry{Registry: "ghcr.io", Repository: "aquaproj/ar2", Username: "someone"},
		},
		{
			// The user defaults to the repository's owner, which is who the token in
			// GitHub Actions belongs to.
			name:  "the username defaults to the owner",
			flags: &state.Flags{Registry: "ghcr.io", Repository: "aquaproj/ar2"},
			want:  &state.Registry{Registry: "ghcr.io", Repository: "aquaproj/ar2", Username: "aquaproj"},
		},
		{
			// GitHub Actions sets GITHUB_REPOSITORY, so a workflow needs no flag.
			name:  "the repository comes from the environment",
			flags: &state.Flags{Registry: "ghcr.io"},
			env:   "aquaproj/ar2",
			want:  &state.Registry{Registry: "ghcr.io", Repository: "aquaproj/ar2", Username: "aquaproj"},
		},
		{
			name:    "no repository anywhere",
			flags:   &state.Flags{Registry: "ghcr.io"},
			wantErr: true,
		},
		{
			name:    "the repository has no owner",
			flags:   &state.Flags{Registry: "ghcr.io", Repository: "ar2"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", tt.env)
			got, err := tt.flags.Resolve()
			if tt.wantErr {
				if err == nil {
					t.Fatal("an error is expected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Resolve is wrong (-want +got):\n%s", diff)
			}
		})
	}
}
