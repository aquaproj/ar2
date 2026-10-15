package g2

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// IgnoredPackage is a package a run doesn't look at, or a set of them.
type IgnoredPackage struct {
	// Name is the package's name, exactly.
	Name string `yaml:"name,omitempty"`
	// NameRegexp is a regular expression the whole name has to match, for leaving alone
	// every package of an organization with one entry: aquasecurity/.* for one whose IP
	// allow list refuses the API to the runners. A run adds the packages aqua-registry
	// gains to the order, so an entry for each would need another every time the
	// organization gains one.
	//
	// It is a field of its own rather than a way of reading Name: a name is full of dots,
	// and read as a regular expression every one of them would match any character.
	NameRegexp string `yaml:"name_regexp,omitempty"`
	// Reason is why, in prose. The next person to wonder why a package isn't in the
	// registry reads this file and nothing else, so an entry without one says only
	// that somebody decided something.
	Reason string `yaml:"reason,omitempty"`

	nameRegexp *regexp.Regexp
}

// compile reads NameRegexp, anchored at both ends: aquasecurity/.* is every package whose
// name starts with aquasecurity/, not every one with aquasecurity/ somewhere in it.
func (p *IgnoredPackage) compile() error {
	if p.NameRegexp == "" || p.nameRegexp != nil {
		return nil
	}
	re, err := regexp.Compile(`^(?:` + p.NameRegexp + `)$`)
	if err != nil {
		return fmt.Errorf("read name_regexp %q of ignored_packages: %w", p.NameRegexp, err)
	}
	p.nameRegexp = re
	return nil
}

// matches says whether the entry is for the package. An expression that doesn't compile
// matches nothing; reading the configuration is what refuses it.
func (p *IgnoredPackage) matches(pkgName string) bool {
	if p.Name != "" && p.Name == pkgName {
		return true
	}
	if p.compile() != nil || p.nameRegexp == nil {
		return false
	}
	return p.nameRegexp.MatchString(pkgName)
}

// IgnorePackage returns the registry's configuration with the package added to
// ignored_packages, and reports whether it was added.
//
// The file is spliced rather than parsed and written back. It is read by people and
// carries comments and folded prose that a round trip through a YAML encoder would
// reformat, so what comes back differs from what went in only by the lines added.
//
// The entry goes at the top of the list. The list is read as a set -- nothing depends on
// the order -- and inserting at the top means the only line that has to be found is the
// one the list starts on, which is the one thing a parser can give exactly.
func IgnorePackage(content, pkgName, reason string) (string, bool, error) {
	cfg := &RegistryConfig{}
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		return "", false, fmt.Errorf("read %s as YAML: %w", RegistryConfigFileName, err)
	}
	for _, ignored := range cfg.IgnoredPackages {
		if ignored != nil && ignored.Name == pkgName {
			return content, false, nil
		}
	}

	entry := ignoredEntry(pkgName, reason)
	line, err := ignoredPackagesLine(content)
	if err != nil {
		return "", false, err
	}
	if line == 0 {
		// A registry that ignores nothing yet. The list is started at the end of the
		// file, where a key added by hand would have gone.
		return strings.TrimRight(content, "\n") + "\nignored_packages:\n" + entry, true, nil
	}
	lines := strings.Split(content, "\n")
	spliced := make([]string, 0, len(lines)+1)
	spliced = append(spliced, lines[:line]...)
	spliced = append(spliced, strings.TrimSuffix(entry, "\n"))
	spliced = append(spliced, lines[line:]...)
	return strings.Join(spliced, "\n"), true, nil
}

// ignoredPackagesLine is the line ignored_packages is on, or 0 when the file has no such
// key.
//
// Parsed rather than searched for, so that the key in a comment or inside a reason isn't
// taken for the key itself.
func ignoredPackagesLine(content string) (int, error) {
	doc := &yaml.Node{}
	if err := yaml.Unmarshal([]byte(content), doc); err != nil {
		return 0, fmt.Errorf("read %s as YAML: %w", RegistryConfigFileName, err)
	}
	if len(doc.Content) == 0 {
		return 0, nil
	}
	mapping := doc.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return 0, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "ignored_packages" {
			return mapping.Content[i].Line, nil
		}
	}
	return 0, nil
}

// ignoredEntry renders one entry the way the file writes them.
func ignoredEntry(pkgName, reason string) string {
	var b strings.Builder
	b.WriteString("  - name: " + pkgName + "\n")
	if reason == "" {
		return b.String()
	}
	// A literal block, which is what the entries already there use: a reason is
	// sentences, and wrapping them is the author's business rather than the encoder's.
	b.WriteString("    reason: |\n")
	for line := range strings.SplitSeq(strings.TrimRight(reason, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}
