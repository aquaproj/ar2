package initcmd

import "errors"

var (
	errTokenRequired      = errors.New("the environment variable GITHUB_TOKEN is required")
	errRepositoryRequired = errors.New("--repository or the environment variable GITHUB_REPOSITORY is required")
	errRepositoryFormat   = errors.New("the container registry repository must be <owner>/<name>")
)
