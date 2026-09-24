// Package state stores ar2's state in a container registry (GHCR).
//
// The state doesn't belong in the aqua-registry-g2 repository: it changes on every
// run and would add a commit each time. GHCR is used the same way
// aqua-registry-updater uses it, as a small object store keyed by a tag.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileName is the name of the state file inside the OCI artifact.
const FileName = "state.json"

// Tag is the tag the state artifact is pushed to.
const Tag = "latest"

// State is ar2's state.
//
// It holds only what is safe to cache. Which versions have been generated is not
// here: that is read from aqua-registry-g2 itself, because a version only counts
// once its pull request is merged, and a record of "already generated" would stop a
// version whose CI failed from ever being retried.
type State struct {
	// SchemaVersion allows the format to change without breaking an older ar2 that
	// reads the state.
	SchemaVersion string    `json:"schema_version"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Packages is keyed by package name.
	Packages map[string]*Package `json:"packages"`
}

// Package is the state of a single package.
type Package struct {
	RepoOwner string `json:"repo_owner,omitempty"`
	RepoName  string `json:"repo_name,omitempty"`
	// Stars orders the work: packages with more stars are processed first.
	// It is not omitempty: a repository with 0 stars must stay distinguishable from
	// one whose star count could not be read, so that the latter can be retried.
	Stars int `json:"stars"`

	// Versions fingerprints the newest versions the last sweep saw upstream.
	//
	// It is what an ETag would have been, over the one thing a sweep cares about.
	// GitHub's own ETag for the release listing changes whenever an asset is
	// downloaded, because the listing carries the count, so it says "something
	// changed" for packages where nothing did — and does it most for the popular
	// ones, which is exactly backwards.
	Versions string `json:"versions,omitempty"`
	// CaughtUp says the registry held every version of that sweep.
	//
	// It is recorded from asking the registry, never from having opened a pull
	// request. A pull request that fails CI is never merged, and a package marked
	// done for work that didn't land would never be looked at again. Versions are
	// not removed from the registry, so what was true when it was written stays
	// true.
	CaughtUp bool `json:"caught_up,omitempty"`
	// Round counts the runs that have reached this package.
	//
	// A run works through the packages in order and stops when its budget is spent,
	// so the ones it never reached keep the lower count and come first next time.
	// Without it the order is the same every run, and a package that takes a share
	// and gets nowhere -- one whose asset can't be downloaded, say -- takes the same
	// share of every run and holds up everything behind it for as long as it is
	// broken.
	//
	// It counts turns rather than naming a round the whole registry is in, so that
	// nothing has to decide when a round ended: a package that has had fewer turns
	// than another is behind, and that is the whole rule. A package the registry has
	// just gained starts from the largest count there is, which is the back of the
	// order; starting from none would put it ahead of everything until it caught up.
	Round int `json:"round,omitempty"`
	// LastDeepCheck is when the package's whole history was last walked. Zero means
	// never, which is what a package that hasn't been backfilled looks like.
	//
	// A sweep sees the newest versions, and anything published after the history is
	// complete appears among them. A release dated in the past doesn't, so the
	// history is walked again from time to time to find what the sweep structurally
	// cannot.
	LastDeepCheck time.Time `json:"last_deep_check,omitzero"`
}

// Fingerprint reduces a sweep's versions to something worth storing for every
// package in the registry.
//
// The versions themselves would be a few hundred kilobytes of state pushed on every
// run; what a sweep asks of them is only whether they are the same as last time.
func Fingerprint(versions []string) string {
	h := sha256.New()
	for _, v := range versions {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SchemaVersion is the current schema version of the state.
const SchemaVersion = "0.1.0"

// New creates an empty state.
func New() *State {
	return &State{
		SchemaVersion: SchemaVersion,
		Packages:      map[string]*Package{},
	}
}

// Write writes the state to path.
func Write(path string, s *State) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:mnd
			return fmt.Errorf("create a directory for the state file: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create a state file: %w", err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s); err != nil {
		return fmt.Errorf("write the state as JSON: %w", err)
	}
	return nil
}

// Read reads a state.
func Read(r io.Reader) (*State, error) {
	s := &State{}
	if err := json.NewDecoder(r).Decode(s); err != nil {
		return nil, fmt.Errorf("read the state as JSON: %w", err)
	}
	if s.Packages == nil {
		s.Packages = map[string]*Package{}
	}
	return s, nil
}
