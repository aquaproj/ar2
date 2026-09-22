package main

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
)

// examplesShown is how many differing resolutions a report prints in full. The names
// of the packages are what the next step needs; the examples are for reading.
const examplesShown = 20

// synthetic compares the two definitions on the versions their constraints mention.
//
// A constraint exists to turn something on at a version, so the versions it names are
// the ones where the two definitions can disagree. Making them up costs nothing and
// needs no network, which is what makes this the first pass.
//
// It says where to look rather than what is wrong. A package that names its versions
// something other than the constraints suggest -- V0.7.0, cli-0.0.0 -- is compared on
// versions it doesn't have, and reports a difference that only exists in the check.
// That is what the real command is for.
func synthetic(w io.Writer, root string) error {
	logger := slog.New(slog.DiscardHandler)
	packages, differ := 0, 0
	byField := map[string]int{}
	var examples []string

	if err := walkPackages(root, func(src *aquaregistry.PackageInfo) {
		packages++
		if comparePackage(logger, src, byField, &examples) {
			differ++
			fmt.Fprintln(w, "PKG", src.GetName())
		}
	}); err != nil {
		return err
	}

	fmt.Fprintln(w, "fields that differed (versions x packages):")
	names := make([]string, 0, len(byField))
	for k := range byField {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(w, "  %-16s %d\n", k, byField[k])
	}
	fmt.Fprintf(w, "\npackages=%d  packages that resolve differently=%d\n\n", packages, differ)
	for _, e := range examples {
		fmt.Fprintln(w, e)
	}
	return nil
}

// comparePackage compares the two definitions of one package on every version its
// constraints mention, and reports whether any of them resolved differently.
func comparePackage(logger *slog.Logger, src *aquaregistry.PackageInfo, byField map[string]int, examples *[]string) bool {
	cfg, _ := migrate.Config(src.Copy(), nil)
	differs := false
	for _, v := range mentionedVersions(src) {
		a, errA := src.Copy().SetVersion(logger, v)
		b, errB := cfg.SetVersion(logger, v)
		if errA != nil || errB != nil {
			continue
		}
		x, y := comparedFields(a), comparedFields(b)
		for _, name := range comparedFieldNames() {
			if x[name] == y[name] {
				continue
			}
			byField[name]++
			differs = true
			if len(*examples) < examplesShown {
				*examples = append(*examples, fmt.Sprintf("%-38s %-8s %-12s v1=%s g2=%s",
					src.GetName(), v, name, oneLine(x[name], exampleWidth), oneLine(y[name], exampleWidth)))
			}
			break
		}
	}
	return differs
}

// mentionedVersions collects the version literals a definition's constraints name.
func mentionedVersions(p *aquaregistry.PackageInfo) []string {
	seen := map[string]struct{}{}
	add := func(constraints string) {
		for _, v := range literals(constraints) {
			seen[v] = struct{}{}
			// Most tags are written with a v, but only where the package doesn't name
			// its versions some other way.
			if p.VersionPrefix == "" && !strings.HasPrefix(v, "v") {
				seen["v"+v] = struct{}{}
			}
		}
	}
	add(p.VersionConstraints)
	for _, vo := range p.VersionOverrides {
		add(vo.VersionConstraints)
	}

	out := make([]string, 0, len(seen))
	for v := range seen {
		// A constraint is evaluated against the version with the prefix taken off, so
		// a synthetic version without one matches no override at all.
		if p.VersionPrefix != "" && !strings.HasPrefix(v, p.VersionPrefix) {
			v = p.VersionPrefix + v
		}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// literals pulls the quoted version strings out of a constraint expression.
func literals(constraints string) []string {
	quoted := regexp.MustCompile(`"([^"]+)"`)
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(constraints, -1) {
		v := strings.TrimSpace(m[1])
		// A bound such as semver(">= 1.2.3") arrives with its operator attached.
		for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
			v = strings.TrimSpace(strings.TrimPrefix(v, op))
		}
		if v == "" || v == "true" || v == "false" {
			continue
		}
		out = append(out, v)
	}
	return out
}
