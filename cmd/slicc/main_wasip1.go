package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ai-ecoverse/slicc-cli/internal/joinurl"
)

var version = "dev"

const usage = `slicc: SLICC's command line, inside seven

usage:
  slicc <join-url> prompt <text>
                            send a prompt to another seven's agent and stream
                            the answer
  slicc <join-url> watch    watch that seven's agent as it works
  slicc <join-url> abort    abort that agent's run
  slicc tray                this seven's tray: its state and linked devices
  slicc tray url|rotate|unlink <device>|retry
                            print or rotate the join URL, drop a device,
                            reconnect (slicc tray --help)
  slicc --version           print the version
  slicc --help              print this help

slicc reaches the other seven through this seven's link facade
(SLICC_LINK_URL), and tray asks it about this seven. slicc's other commands
aren't available inside seven.
`

func unavailable(stderr io.Writer, command string) int {
	fmt.Fprintf(stderr, "slicc: %s isn't available inside seven\n", command)
	return 2
}

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
	case "tray":
		return runTray(args[1:], stdout, stderr)
	}
	if isJoinRef(args[0]) {
		join, err := readJoinRef(args[0], os.Stdin)
		if err == nil {
			_, err = joinurl.Parse(join)
		}
		if err != nil {
			fmt.Fprintf(stderr, "slicc: %v\n", err)
			return 2
		}
		args = append([]string{join}, args[1:]...)
	}
	if !looksLikeURL(args[0]) {
		return unavailable(stderr, args[0])
	}
	if _, err := joinurl.Parse(args[0]); err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
		return 2
	}
	if len(args) < 2 {
		fmt.Fprint(stderr, "slicc: a join URL must be followed by a command: prompt, watch or abort\n")
		return 2
	}
	switch args[1] {
	case "prompt", "watch", "abort":
		return runAgent(args[1], args[0], args[2:], agentIO{stdin: os.Stdin, stdout: stdout, stderr: stderr})
	}
	return unavailable(stderr, args[1])
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
