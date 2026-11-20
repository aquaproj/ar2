package verify

import (
	"crypto/md5"  //nolint:gosec // a checksum file may list MD5, and what it lists is what is compared
	"crypto/sha1" //nolint:gosec // a checksum file may list SHA1, and what it lists is what is compared
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
	"sync"
)

// The algorithms a checksum file may list, as aqua names them.
const (
	algoMD5    = "md5"
	algoSHA1   = "sha1"
	algoSHA256 = "sha256"
	algoSHA512 = "sha512"
)

// algorithmOf says which algorithm a digest written in hex is from, by its length, or
// returns an empty string for a length none of them has.
//
// A checksum file's name doesn't say reliably: caddy_2.11.9_checksums.txt lists SHA512,
// where a name with no algorithm in it is taken for SHA256. The digest itself does, since
// each algorithm's is a length of its own.
func algorithmOf(digest string) string {
	switch len(digest) {
	case md5.Size * 2: //nolint:mnd // two hex digits a byte
		return algoMD5
	case sha1.Size * 2: //nolint:mnd // two hex digits a byte
		return algoSHA1
	case sha256.Size * 2: //nolint:mnd // two hex digits a byte
		return algoSHA256
	case sha512.Size * 2: //nolint:mnd // two hex digits a byte
		return algoSHA512
	}
	return ""
}

// hasher computes every digest a checksum file may list while the asset streams past, so
// the asset is downloaded once whatever the file turns out to list.
type hasher struct {
	hashes map[string]hash.Hash
}

func newHasher() *hasher {
	return &hasher{hashes: map[string]hash.Hash{
		algoMD5:    md5.New(),  //nolint:gosec // see the import
		algoSHA1:   sha1.New(), //nolint:gosec // see the import
		algoSHA256: sha256.New(),
		algoSHA512: sha512.New(),
	}}
}

// writer is what the asset is copied through.
func (h *hasher) writer() io.Writer {
	ws := make([]io.Writer, 0, len(h.hashes))
	for _, w := range h.hashes {
		ws = append(ws, w)
	}
	return io.MultiWriter(ws...)
}

// sums returns each digest in hex, by algorithm.
func (h *hasher) sums() map[string]string {
	out := make(map[string]string, len(h.hashes))
	for algo, w := range h.hashes {
		out[algo] = hex.EncodeToString(w.Sum(nil))
	}
	return out
}

// digests holds what each downloaded asset hashes to, by URL and then by algorithm.
type digests struct {
	mu   sync.Mutex
	sums map[string]map[string]string
}

func (d *digests) record(url string, sums map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sums == nil {
		d.sums = map[string]map[string]string{}
	}
	d.sums[url] = sums
}

// of returns what the asset at url hashes to under algo, or an empty string when it wasn't
// downloaded.
func (d *digests) of(url, algo string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sums[url][algo]
}
