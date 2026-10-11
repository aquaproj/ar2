package initcmd

import "errors"

var errTokenRequired = errors.New("a GitHub access token is required: set GITHUB_TOKEN or set up ghtkn")
