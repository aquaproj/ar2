package generate

import (
	"testing"
	"time"
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
