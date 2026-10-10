package generate

import (
	"testing"
	"time"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

// A release says when it was published, and that is written the one way a reader can
// compare: RFC 3339, in UTC. A release with no such moment says nothing rather than
// saying the beginning of the epoch.
func TestPublishedAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "a release in another zone is written in UTC",
			at:   time.Date(2026, 10, 1, 9, 30, 0, 0, time.FixedZone("JST", 9*60*60)),
			want: "2026-10-01T00:30:00Z",
		},
		{
			name: "no moment",
			at:   time.Time{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := publishedAt(tt.at); got != tt.want {
				t.Errorf("publishedAt is %q, want %q", got, tt.want)
			}
		})
	}
}

// Which forge a package is on decides where its releases are read from, and a type can
// name the instance by default: a gitlab_release package is on gitlab.com unless it says
// otherwise, so a definition on it says no host at all.
func TestOnInstance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		base *aquaregistry.PackageInfo
		want bool
	}{
		{
			name: "no definition",
		},
		{
			name: "a GitHub release",
			base: &aquaregistry.PackageInfo{Type: "github_release", RepoOwner: "cli", RepoName: "cli"},
		},
		{
			name: "a release on a Forgejo instance",
			base: &aquaregistry.PackageInfo{Type: "forgejo_release", Host: "codeberg.org"},
			want: true,
		},
		{
			name: "a release on a Gitea instance",
			base: &aquaregistry.PackageInfo{Type: "gitea_release", Host: "gitea.com"},
			want: true,
		},
		{
			name: "a GitLab project saying which instance",
			base: &aquaregistry.PackageInfo{Type: "gitlab_release", Host: "gitlab.example.com"},
			want: true,
		},
		{
			name: "a GitLab project on the instance the type means",
			base: &aquaregistry.PackageInfo{Type: "gitlab_release"},
			want: true,
		},
		{
			// A type that is on no instance has nothing to read from one, host
			// or not.
			name: "an http package naming a host",
			base: &aquaregistry.PackageInfo{Type: "http", Host: "codeberg.org"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := OnInstance(tt.base); got != tt.want {
				t.Errorf("OnInstance is %v, want %v", got, tt.want)
			}
		})
	}
}
