// Package g2 talks to the aqua-registry-g2 repository.
package g2

import (
	"fmt"
	"strings"
)

// BranchPrefix marks the branches holding a package's generated registry.json.
// It keeps them out of the way of main and of any operational branch, and lets one
// branch ruleset cover all of them with a single pattern.
const BranchPrefix = "pkg_"

// BranchName returns the branch holding the package's generated registry.json.
func BranchName(pkgName string) string {
	return BranchPrefix + EncodePackageName(pkgName)
}

// EncodePackageName escapes a package name so it can be used as a git ref.
//
// Every character outside [A-Za-z0-9.-] becomes an underscore followed by two
// lowercase hex digits. Escaping the underscore itself as well makes the mapping
// reversible, which "/" -> "__" is not: 12 package names already contain an
// underscore, so that scheme is ambiguous in principle.
//
//	cli/cli                    -> cli_2fcli
//	ipinfo/cli/grepip          -> ipinfo_2fcli_2fgrepip
//	sr.ht/~charles/rq          -> sr.ht_2f_7echarles_2frq
//	po3rin/github_link_creator -> po3rin_2fgithub_5flink_5fcreator
//
// The result stays within [A-Za-z0-9._-], so it needs no further escaping in a URL.
// Percent-encoding would not: a branch named with "%7E" has to be written "%257E" in
// a raw URL, because the server decodes the escape before looking the ref up.
//
// The encoded name also contains no "/", so it is a single ref segment. That removes
// the directory/file conflict that would otherwise make "ipinfo/cli" and
// "ipinfo/cli/grepip" unable to coexist as branches; aqua-registry has 30 such pairs.
func EncodePackageName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, c := range []byte(name) {
		if isSafe(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "_%02x", c)
	}
	return b.String()
}

func isSafe(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c >= '0' && c <= '9',
		c == '.', c == '-':
		return true
	}
	return false
}

// HeadBranchPrefix marks the branches ar2 opens pull requests from.
// They are separate from the package branches so that a ruleset protecting the
// latter doesn't have to make an exception for the branch a pull request comes from.
const HeadBranchPrefix = "ar2_"

// HeadBranchName returns the branch a pull request for the package is opened from.
//
// It carries no version: one pull request adds every version of the package that a
// run found, which keeps the number of pull requests down and, more importantly,
// keeps two of them from targeting the same package branch at once. Two would
// conflict on merge, since the second is written against a base the first has moved.
func HeadBranchName(pkgName string) string {
	return HeadBranchPrefix + EncodePackageName(pkgName)
}
