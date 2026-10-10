package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `slicc: SLICC's command line for seven

usage:
  slicc update [--check]    replace this binary with the latest release
  slicc --version           print the version
  slicc --help              print this help
`

func run(args []string, stdout, stderr io.Writer) int {
	defer startup(args, stderr)()
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
	case "update":
		return runUpdate(args[1:], stdout, stderr)
	default:
		if looksLikeURL(args[0]) {
			return joinCommand(args, stdout, stderr)
		}
		fmt.Fprintf(stderr, "slicc: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
