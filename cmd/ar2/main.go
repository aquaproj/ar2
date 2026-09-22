package main

import (
	"github.com/aquaproj/ar2/pkg/cli"
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
)

var version = ""

func main() {
	cobrautil.Main("ar2", version, cli.Run)
}
