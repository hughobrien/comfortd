// SPDX-License-Identifier: GPL-3.0-or-later

// Command comfortd bridges Mitsubishi Comfort (Kumo Cloud) air conditioners.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

var version = "dev"

func run(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("comfortd", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
