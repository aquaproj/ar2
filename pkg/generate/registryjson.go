// Package generate builds aqua-registry-g2's registry.json from an upstream release.
package generate

// Registry is the content of versions/<version>/registry.json.
// Unlike aqua-registry (v1), nothing here is a template: every field is already
// resolved for one os/arch, so aqua can install the package without evaluating
// anything.
type Registry struct {
	Assets []*Asset `json:"assets"`
}

// Asset is everything needed to install the package on one os/arch.
type Asset struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Variants distinguishes rows that share an os/arch but differ otherwise, such
	// as a linux/amd64 build for musl and one for glibc. aqua models these as
	// key/value pairs, and "libc" is the only key it evaluates today.
	// It is omitted when the package has no variant-aware override.
	Variants map[string]string `json:"variants,omitempty"`
	Type     string            `json:"type"`

	RepoOwner string `json:"repo_owner,omitempty"`
	RepoName  string `json:"repo_name,omitempty"`
	Asset     string `json:"asset,omitempty"`
	// URL is what an http package is downloaded from. github_release packages are
	// identified by Asset instead.
	URL    string `json:"url,omitempty"`
	Format string `json:"format,omitempty"`

	// Checksum is empty when the release predates GitHub's asset digests and the
	// asset hasn't been downloaded to hash it.
	Checksum string `json:"checksum,omitempty"`
	// ChecksumAlgorithm is always sha256 for digests taken from the release API.
	ChecksumAlgorithm string `json:"checksum_algorithm,omitempty"`

	Files []*File `json:"files,omitempty"`

	Cosign                     any `json:"cosign,omitempty"`
	GitHubArtifactAttestations any `json:"github_artifact_attestations,omitempty"`
}

// File is an executable inside the asset.
type File struct {
	Name string `json:"name"`
	Src  string `json:"src,omitempty"`
}
