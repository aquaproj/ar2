// Package state stores ar2's state in a container registry (GHCR).
//
// The state doesn't belong in the aqua-registry-g2 repository: it changes on every
// run and would add a commit each time. GHCR is used the same way
// aqua-registry-updater uses it, as a small object store keyed by a tag.
package state

import (
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
