// Package validateindex checks that index.json says what aqua reads it for.
//
// The catalogue is one file holding the name and description of every package the
// registry has, because choosing a package means reading all of them before knowing
// which one is wanted. It is written by ar2 and merged without a reader, so this is
// where a file that would break searching is caught.
package validateindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// Validate reads the catalogue at path and reports what is wrong with it.
//
// Everything wrong is reported rather than the first thing: a file written by a
// program is usually wrong in the same way throughout, and a reader who fixes what
// the program does wants to see all of it.
func Validate(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open the catalogue: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	// A field aqua wouldn't read means the file was written by something that
	// disagrees with aqua about what a catalogue is.
	dec.DisallowUnknownFields()
	index := &aquag2.Index{}
	if err := dec.Decode(index); err != nil {
		return fmt.Errorf("read the catalogue as JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errTrailingContent
	}
	if index.Packages == nil {
		return errNoPackages
	}

	if problems := entries(index); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(w, p)
		}
		return errInvalid
	}

	fmt.Fprintf(w, "%s: %d packages\n", path, len(index.Packages))
	return nil
}

// entries reports what is wrong with the packages, as lines to print.
func entries(index *aquag2.Index) []string {
	var problems []string
	seen := make(map[string]struct{}, len(index.Packages))
	for i, pkg := range index.Packages {
		switch {
		case pkg == nil:
			problems = append(problems, fmt.Sprintf("packages[%d] is null", i))
			continue
		case pkg.Name == "":
			problems = append(problems, fmt.Sprintf("packages[%d] has no name", i))
			continue
		}
		// A package listed twice is shown twice, and the second one is what a search
		// would never explain.
		if _, ok := seen[pkg.Name]; ok {
			problems = append(problems, pkg.Name+" is listed more than once")
		}
		seen[pkg.Name] = struct{}{}
	}
	return problems
}
