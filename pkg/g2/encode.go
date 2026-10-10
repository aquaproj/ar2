// Package g2 talks to the aqua-registry-g2 repository.
package g2

import (
	"fmt"
	"strings"
)

// PackagesDir is the directory on the default branch holding every package.
const PackagesDir = "pkgs"

// DefaultBranch is the branch the registry is read from and every pull request targets.
const DefaultBranch = "main"

// PackageDir returns the directory holding the package whose id this is, such as
// pkgs/69/1790772769.
//
// The packages are spread over a hundred directories rather than kept in one, because a
// directory is a tree object that every commit under it writes again: one holding thousands
// of packages would be rewritten whole by every version added to any of them, and GitHub
// recommends keeping a directory under 3,000 entries.
//
// What spreads them is the id's last two digits. An id is the second it was minted, so its
// leading digits are a time bucket -- every package taken over in the same few years shares
// them -- while its trailing digits are as good as uniform. The leaf is the whole id rather
// than what follows the shard, so an id read out of names.json is found as it is.
func PackageDir(id string) string {
	return PackagesDir + "/" + Shard(id) + "/" + id
}

// shardWidth is how many of an id's trailing digits name its shard: a hundred directories.
const shardWidth = 2

// Shard is the directory under PackagesDir the package whose id this is sits in.
func Shard(id string) string {
	if len(id) < shardWidth {
		return strings.Repeat("0", shardWidth-len(id)) + id
	}
	return id[len(id)-shardWidth:]
}

// IsID reports whether s is an id: a decimal number.
func IsID(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// HeadBranchID returns the id of the package a head branch is for, and false for a branch
// that isn't one of a package's: the catalogue's, or one ar2 didn't make.
//
// Both the package's own head branch and one of its version branches name it, the second
// followed by the version.
func HeadBranchID(branch string) (string, bool) {
	rest, ok := strings.CutPrefix(branch, HeadBranchPrefix)
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "_")
	if !IsID(id) {
		return "", false
	}
	return id, true
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
// They are separate from the default branch so that a ruleset protecting it doesn't
// have to make an exception for the branch a pull request comes from.
const HeadBranchPrefix = "ar2_"

// HeadBranchName returns the branch a pull request for the package with this id is opened
// from.
//
// It carries no version: one pull request adds every version of the package that a
// run found, which keeps the number of pull requests down and, more importantly,
// keeps two of them from writing the same package's versions.json at once. Two would
// conflict on merge, since the second is written against a base the first has moved.
func HeadBranchName(id string) string {
	return HeadBranchPrefix + id
}

// VersionHeadBranchName returns the branch a pull request for one version alone is opened
// from.
//
// One version has a branch of its own when what was generated for it can't merge: it names a
// file the archive doesn't hold, so the definition has to say something it doesn't, and until
// somebody writes that the version waits. In the pull request with the rest it would keep the
// versions that were right from merging, which is the whole reason for a branch per version
// here and one per package everywhere else.
//
// Two of these don't conflict with each other or with the package's own, because a pull
// request moves the default branch only when it merges, and this one doesn't until a
// person has been.
func VersionHeadBranchName(id, version string) string {
	return HeadBranchName(id) + "_" + EncodePackageName(version)
}

// IsVersionHeadBranch reports whether the branch is one of the package's version branches.
//
// A prefix match isn't enough, because one id can be the beginning of another: 1790772680
// starts with 179077268. What tells them apart is what follows the separator -- an encoded
// version, in which an underscore only ever begins an escape.
func IsVersionHeadBranch(id, branch string) bool {
	prefix := HeadBranchName(id) + "_"
	if !strings.HasPrefix(branch, prefix) {
		return false
	}
	return isEncoded(strings.TrimPrefix(branch, prefix))
}

// isEncoded reports whether s is what EncodePackageName produces: characters it leaves
// alone, and underscores each beginning a two-digit hex escape.
func isEncoded(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' {
			if !isSafe(c) {
				return false
			}
			continue
		}
		if i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
			return false
		}
		i += 2
	}
	return true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}

// RemoveBranchName returns the branch the pull request that stops serving the package is
// opened from.
//
// Its own branch rather than the one a version's pull request uses, because it goes the
// other way: into the default branch, carrying the registry's configuration and the
// catalogue. A package could have both open at once, and they must not be the same ref.
func RemoveBranchName(id string) string {
	return HeadBranchPrefix + "remove_" + id
}
