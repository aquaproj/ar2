package verify

import (
	"io"
	"os"
)

// downloadedFile adapts a local file to the interface aqua's unarchiver expects.
type downloadedFile struct {
	path string
}

func (f *downloadedFile) Path() (string, error) {
	return f.path, nil
}

func (f *downloadedFile) ReadLast() (io.ReadCloser, error) {
	return os.Open(f.path) //nolint:wrapcheck
}

// Wrap would let the unarchiver report progress. Nothing here reports progress, so
// the writer is returned as it is.
func (f *downloadedFile) Wrap(w io.Writer) io.Writer {
	return w
}
