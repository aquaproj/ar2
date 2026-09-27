// Package flag defines the flags shared by ar2's commands.
package flag

import "github.com/spf13/pflag"

// GlobalFlags holds the flags every command accepts, and what every command knows.
type GlobalFlags struct {
	LogLevel string
	// Version is the ar2 that is running, which the pull requests it opens are labelled
	// with. It is not a flag: it comes from the binary rather than the command line.
	Version string
}

// LogLevel registers the --log-level flag.
func LogLevel(fs *pflag.FlagSet, p *string) {
	fs.StringVar(p, "log-level", "", "log level (debug, info, warn, error)")
}
