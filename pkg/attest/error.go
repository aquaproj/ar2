package attest

import "errors"

// errPkgName is what a package name that isn't "<owner>/<name>" gets. Nothing
// generates one, so this is a programming error rather than something a run meets.
var errPkgName = errors.New("a package name must be <repo owner>/<repo name>")
