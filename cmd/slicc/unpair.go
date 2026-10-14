//go:build !wasip1

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/loopback"
)

const unpairUsage = `usage: slicc unpair [<fingerprint> | --all]

Forget sevens paired with slicc open or slicc pair. Without arguments it
lists them: the page's fingerprint, its origin and when it paired. With a
fingerprint (or a unique prefix of it, colons and case ignored) it forgets
that one, and with --all every one. A forgotten seven can't link again
until it pairs with a new pairing URL; a link that is up stays up until it
drops.
`

func runUnpair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unpair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, unpairUsage)
			return 0
		}
		fmt.Fprintf(stderr, "slicc unpair: %v\n\n%s", err, unpairUsage)
		return 2
	}
	if fs.NArg() > 1 || (*all && fs.NArg() > 0) {
		fmt.Fprintf(stderr, "slicc unpair: name one fingerprint, or --all\n\n%s", unpairUsage)
		return 2
	}
	dir, err := loopback.DefaultDir()
	if err != nil {
		fmt.Fprintln(stderr, "slicc unpair: no user config directory")
		return 1
	}
	peers := loopback.OpenPeers(dir)
	switch {
	case *all:
		gone, err := peers.RemoveAll()
		if err != nil {
			fmt.Fprintf(stderr, "slicc unpair: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "forgot %d paired sevens\n", len(gone))
		return 0
	case fs.NArg() == 1:
		return unpairOne(peers, fs.Arg(0), stdout, stderr)
	}
	list, err := peers.List()
	if err != nil {
		fmt.Fprintf(stderr, "slicc unpair: %v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "no paired sevens")
		return 0
	}
	for _, p := range list {
		fmt.Fprintf(stdout, "%s  %s  paired %s\n", p.Fingerprint, p.Origin, p.FirstSeen)
	}
	return 0
}

func unpairOne(peers *loopback.Peers, prefix string, stdout, stderr io.Writer) int {
	p, err := peers.Remove(prefix)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(stderr, "slicc unpair: no paired seven matches %s\n", prefix)
		return 1
	case errors.Is(err, loopback.ErrAmbiguous):
		fmt.Fprintf(stderr, "slicc unpair: more than one paired seven matches %s; give more of the fingerprint\n", prefix)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "slicc unpair: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "forgot %s (%s)\n", loopback.ShortFingerprint(p.Fingerprint), p.Origin)
	return 0
}
