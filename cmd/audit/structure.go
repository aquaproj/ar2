package main

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/ar2/pkg/migrate"
)

// examplesPerKind is how many packages a report names for each kind of finding. The
// counts say how much there is; the examples say what it looks like.
const examplesPerKind = 3

type finding struct {
	kind   string
	pkg    string
	detail string
}

// structure converts every package and counts what the conversion produced.
//
// Nothing here compares resolutions. These are the properties an output must have
// whatever the input was: constraints that evaluate, files that survive, overrides
// that say something.
func structure(w io.Writer, root string) error {
	logger := slog.New(slog.DiscardHandler)
	counts := map[string]int{}
	var findings []finding
	packages := 0

	report := func(kind, pkg, detail string) {
		counts[kind]++
		findings = append(findings, finding{kind: kind, pkg: pkg, detail: detail})
	}

	if err := walkPackages(root, func(src *aquaregistry.PackageInfo) {
		packages++
		checkPackage(logger, src, counts, report)
	}); err != nil {
		return err
	}

	fmt.Fprintf(w, "packages: %d\n\n", packages)
	printCounts(w, counts)
	printExamples(w, findings)
	return nil
}

// checkPackage converts one package and reports what the conversion produced.
func checkPackage(logger *slog.Logger, src *aquaregistry.PackageInfo, counts map[string]int, report func(kind, pkg, detail string)) {
	name := src.GetName()
	cfg, unconverted := migrate.Config(src.Copy(), nil)
	out := cfg.PackageInfo

	if len(unconverted) > 0 {
		// Not a fault. A definition whose constraints can't all be read keeps its
		// original order, and this is how many took that path.
		report("unconverted-constraint", name, strings.Join(unconverted, " | "))
	}
	checkConstraints(logger, out, name, report)
	if hasFiles(src) && !hasFiles(out) {
		// files can't be read off a release, so losing them loses the package.
		report("files-lost", name, "")
	}
	if src.Description != "" && out.Description == "" {
		report("description-lost", name, "")
	}
	if src.Description == "" {
		counts["no-description-in-source"]++
	}
	if len(out.VersionOverrides) > 1 && allEmpty(out.VersionOverrides) {
		// A list of overrides that all say nothing should have collapsed into one.
		report("empty-overrides-kept", name, strconv.Itoa(len(out.VersionOverrides)))
	}
	if len(out.VersionOverrides) == 0 {
		report("no-override", name, "")
	}
}

// checkConstraints evaluates every constraint the conversion wrote. A constraint is
// an expression, and one that isn't an expression fails at install time rather than
// here, where nobody is watching.
func checkConstraints(logger *slog.Logger, out *aquaregistry.PackageInfo, name string, report func(kind, pkg, detail string)) {
	for _, vo := range out.VersionOverrides {
		_, err := expr.EvaluateVersionConstraints(logger, vo.VersionConstraints, "v1.0.0", "1.0.0")
		if err == nil {
			continue
		}
		// A constraint that reads a field this version doesn't have is fine; one that
		// doesn't parse, or answers with something other than a boolean, is not.
		if strings.Contains(err.Error(), "expected bool") || strings.Contains(err.Error(), "parse") {
			report("constraint-not-an-expression", name, vo.VersionConstraints+": "+err.Error())
		}
	}
}

func printCounts(w io.Writer, counts map[string]int) {
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(w, "%-32s %d\n", k, counts[k])
	}
	fmt.Fprintln(w)
}

func printExamples(w io.Writer, findings []finding) {
	shown := map[string]int{}
	for _, f := range findings {
		if f.kind == "no-description-in-source" || shown[f.kind] >= examplesPerKind {
			continue
		}
		shown[f.kind]++
		fmt.Fprintf(w, "%-32s %-40s %s\n", f.kind, f.pkg, oneLine(f.detail, detailWidth))
	}
}

func hasFiles(p *aquaregistry.PackageInfo) bool {
	if len(p.Files) > 0 {
		return true
	}
	for _, vo := range p.VersionOverrides {
		if len(vo.Files) > 0 {
			return true
		}
	}
	return false
}

// allEmpty reports whether every override says nothing beyond the version it applies
// to.
func allEmpty(overrides []*aquaregistry.VersionOverride) bool {
	for _, vo := range overrides {
		if vo == nil {
			continue
		}
		copied := *vo
		copied.VersionConstraints = ""
		if !isZero(copied) {
			return false
		}
	}
	return true
}

func isZero(vo aquaregistry.VersionOverride) bool {
	return render(vo) == render(aquaregistry.VersionOverride{})
}
