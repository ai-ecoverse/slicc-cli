package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `slicc: SLICC's command line for seven

usage:
  slicc [flags]             find browsers, open seven in the best one and link
                            it; flags below
  slicc attach --cdp <url>  link a seven open in a running CDP browser
  slicc <join-url> follow   follow a seven's tray from this machine
  slicc browsers            list CDP endpoints and installed browsers, ranked
  slicc launch              launch a browser with CDP in a separate SLICC profile
  slicc update [--check]    replace this binary with the latest release
  slicc --version           print the version
  slicc --help              print this help
` + defaultHelp

func run(args []string, stdout, stderr io.Writer) int {
	defer startup(args, stderr)()
	if isDefault(args) {
		return runDefault(args, stdout, stderr)
	}
	switch args[0] {
	case "--version", "-V", "version":
		fmt.Fprintf(stdout, "slicc %s\n", version)
		return 0
	case "browsers":
		return runBrowsers(args[1:], stdout, stderr)
	case "launch":
		return runLaunch(args[1:], stdout, stderr)
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "attach":
		return attach(args[1:], os.Stdin, stdout, stderr)
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
