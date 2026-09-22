// Package main provides audit, a development tool for checking what the conversion
// from a v1 aqua-registry definition to a g2 one does.
//
// The conversion is a rewrite of every package in aqua-registry, and reading the
// output tells you little: a definition can look reasonable and still resolve
// differently from the one it replaces. These checks answer the only question that
// matters, which is whether aqua would install anything different.
//
//	audit structure <aqua-registry>/pkgs
//	audit synthetic <aqua-registry>/pkgs
//	audit real      <aqua-registry>/pkgs <package>...
//	audit one       <registry.yaml> <version>
//
// The usual order is structure, then synthetic, then real on whatever synthetic
// reported, then one on whatever real reported.
package main

import (
	"errors"
	"fmt"
	"os"
)

var errArgs = errors.New("wrong number of arguments")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "audit:", err)
		os.Exit(1)
	}
}

const usage = `audit checks what the v1 to g2 conversion does.

  audit structure <aqua-registry>/pkgs            count what the conversion produces
  audit synthetic <aqua-registry>/pkgs            compare resolutions on the versions the constraints mention
  audit real      <aqua-registry>/pkgs <package>  compare resolutions on the versions the package really has
  audit one       <registry.yaml> <version>       show both resolutions of one package
`

func run(args []string, stdout *os.File) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}
	cmd, ok := commands()[args[0]]
	if !ok {
		fmt.Fprint(stdout, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
	return cmd(stdout, args[1:])
}

// commands maps a name to what it does. Each one checks its own arguments, so that
// adding a command doesn't make the dispatch any harder to read.
func commands() map[string]func(stdout *os.File, args []string) error {
	return map[string]func(stdout *os.File, args []string) error{
		"structure": func(stdout *os.File, args []string) error {
			if len(args) != 1 {
				return errArgs
			}
			return structure(stdout, args[0])
		},
		"synthetic": func(stdout *os.File, args []string) error {
			if len(args) != 1 {
				return errArgs
			}
			return synthetic(stdout, args[0])
		},
		"real": func(stdout *os.File, args []string) error {
			if len(args) < 2 { //nolint:mnd // a registry and at least one package
				return errArgs
			}
			return realTags(stdout, args[0], args[1:])
		},
		"one": func(stdout *os.File, args []string) error {
			if len(args) != 2 { //nolint:mnd // a file and a version
				return errArgs
			}
			return one(stdout, args[0], args[1])
		},
	}
}
