package initcmd

import "errors"

var errTokenRequired = errors.New("the environment variable GITHUB_TOKEN is required")
