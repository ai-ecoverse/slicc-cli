//go:build !wasip1

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
  slicc open                link seven in your everyday browser, without CDP:
                            open a pairing URL in the default browser
                            (also slicc --default-browser)
  slicc pair                the same, but only print the pairing URL
  slicc unpair [--all]      list or forget sevens paired with open or pair
  slicc <join-url> follow   follow a seven's tray from this machine
  slicc tray [-a] [-q]      list the slicc processes here that hold a link
  slicc <join-url> prompt <text>
                            send a prompt to seven's agent and stream the answer
  slicc <join-url> watch    follow seven's agent as it works
  slicc <join-url> abort    abort the agent's run
                            (prompt, watch and abort also take --cdp <url>
                            instead of a join URL)
  slicc browsers            list CDP endpoints and installed browsers, ranked
  slicc launch              launch a browser with CDP in a separate SLICC profile
  slicc update [--check]    replace this binary with the latest release
  slicc install jerk        install slicc-jerk into slicc's data folder
  slicc --version           print the version
  slicc --help              print this help
` + defaultHelp

func run(args []string, stdout, stderr io.Writer) int {
	companionsOnPath()
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
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "open", "pair":
		return runOpen(args[0], args[1:], os.Stdin, stdout, stderr)
	case "unpair":
		return runUnpair(args[1:], stdout, stderr)
	case "tray":
		return runTray(args[1:], stdout, stderr)
	case "prompt", "watch", "abort":
		return runAgent(args[0], "", args[1:], agentIO{stdin: os.Stdin, stdout: stdout, stderr: stderr})
	default:
		if looksLikeURL(args[0]) {
			return joinCommand(args, stdout, stderr)
		}
		if six := sixFlagIn(args); six != "" {
			return sixPointer(stderr, six, args)
		}
		fmt.Fprintf(stderr, "slicc: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
