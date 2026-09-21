package state

import "errors"

var (
	errRepositoryRequired = errors.New("--repository or the environment variable GITHUB_REPOSITORY is required")
	errRepositoryFormat   = errors.New("the container registry repository must be <owner>/<name>")
)
