package generate

import "errors"

var (
	errNoPackage      = errors.New("aqua gr returned no package")
	errPkgNameFormat  = errors.New("the package name must be <repo_owner>/<repo_name>")
	errNoSupportedEnv = errors.New("the package supports no environment")
)
