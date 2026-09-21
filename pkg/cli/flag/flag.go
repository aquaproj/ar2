// Package flag defines the flags shared by ar2's commands.
package flag

import "github.com/spf13/pflag"

// GlobalFlags holds the flags every command accepts.
type GlobalFlags struct {
	LogLevel string
}

// LogLevel registers the --log-level flag.
func LogLevel(fs *pflag.FlagSet, p *string) {
	fs.StringVar(p, "log-level", "", "log level (debug, info, warn, error)")
}
