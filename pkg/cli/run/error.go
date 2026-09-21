package run

import "errors"

var (
	errTokenRequired     = errors.New("the environment variable GITHUB_TOKEN is required")
	errVersionRequired   = errors.New("the version is required: ar2 run <package name>@<version>")
	errSkipPRRequired    = errors.New("--skip-pr is required: creating a pull request isn't implemented yet")
	errOutputDirRequired = errors.New("--output-dir is required when no package is given")
)
