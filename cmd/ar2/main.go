package main

import (
	"github.com/suzuki-shunsuke/cobra-util/cobrautil"
	"github.com/szksh-lab-2/ar2/pkg/cli"
)

var version = ""

func main() {
	cobrautil.Main("ar2", version, cli.Run)
}
