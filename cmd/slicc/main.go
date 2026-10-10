package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `slicc: SLICC's command line for seven

usage:
  slicc --version    print the version
  slicc --help       print this help
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--version", "-V", "version":
		fmt.Fprintf(stdout, "slicc %s\n", version)
		return 0
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "slicc: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
