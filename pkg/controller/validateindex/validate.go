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
	return append(problems, aliases(index, seen)...)
}

// aliases reports the aliases that don't name one package.
//
// An alias is another name for a package, usually the one its repository had before it
// was renamed, and it is how someone whose aqua.yaml still says the old name reaches
// the package at all: what resolves it is a table built from these, and a table needs
// one answer per name.
//
// Which answer is right can't be settled by a rule -- reading the real package first,
// say -- because a name that is both is a mistake in the registry either way, and a
// rule would give that mistake a meaning. So it is reported here instead, where the
// catalogue is written rather than where it is read.
func aliases(index *aquag2.Index, packages map[string]struct{}) []string {
	var problems []string
	// The package each alias names, so that two packages claiming one alias are
	// reported against the second of them rather than counted twice.
	claimed := map[string]string{}
	for _, pkg := range index.Packages {
		if pkg == nil {
			continue
		}
		for _, alias := range pkg.Aliases {
			switch {
			case alias == "":
				problems = append(problems, pkg.Name+" has an alias with no name")
			case alias == pkg.Name:
				problems = append(problems, pkg.Name+" is its own alias")
			case len(packages) > 0 && isPackage(packages, alias):
				problems = append(problems,
					alias+" is an alias of "+pkg.Name+" and a package of its own")
			default:
				if first, ok := claimed[alias]; ok {
					problems = append(problems,
						alias+" is an alias of both "+first+" and "+pkg.Name)
					continue
				}
				claimed[alias] = pkg.Name
			}
		}
	}
	return problems
}

// isPackage reports whether the catalogue lists a package under that name.
func isPackage(packages map[string]struct{}, name string) bool {
	_, ok := packages[name]
	return ok
}
