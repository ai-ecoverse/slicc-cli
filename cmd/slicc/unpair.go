//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/loopback"
)

const unpairSchema = 1

const unpairUsage = `usage: slicc unpair [<fingerprint> | --all] [--json]

Forget sevens paired with slicc open or slicc pair. Without arguments it
lists them: the page's fingerprint, its origin and when it paired. With a
fingerprint (or a unique prefix of it, colons and case ignored) it forgets
that one, and with --all every one. A forgotten seven can't link again
until it pairs with a new pairing URL, and a link that is up closes at once
when the slicc open or pair that holds it knows how (slicc from this
version on); an older one keeps its link until it drops.

  --json   print one JSON document (see README.md)
`

type unpairPeer struct {
	Fingerprint string `json:"fingerprint"`
	Origin      string `json:"origin"`
	Node        string `json:"node"`
	FirstSeen   string `json:"firstSeen"`
	Live        bool   `json:"live"`
}

type unpairList struct {
	Schema int          `json:"schema"`
	Peers  []unpairPeer `json:"peers"`
}

type unpairResult struct {
	Schema  int      `json:"schema"`
	Removed []string `json:"removed"`
	Closed  []string `json:"closed"`
}

func runUnpair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unpair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "")
	asJSON := fs.Bool("json", false, "")
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
		closed := revokeLinks(gone)
		if *asJSON {
			return printUnpair(stdout, gone, closed)
		}
		fmt.Fprintf(stdout, "forgot %d paired sevens\n", len(gone))
		printClosed(stderr, closed)
		return 0
	case fs.NArg() == 1:
		return unpairOne(peers, fs.Arg(0), *asJSON, stdout, stderr)
	}
	list, err := peers.List()
	if err != nil {
		fmt.Fprintf(stderr, "slicc unpair: %v\n", err)
		return 1
	}
	if *asJSON {
		live := livePeers()
		out := unpairList{Schema: unpairSchema, Peers: []unpairPeer{}}
		for _, p := range list {
			out.Peers = append(out.Peers, unpairPeer{Fingerprint: p.Fingerprint, Origin: p.Origin, Node: p.Node, FirstSeen: p.FirstSeen, Live: live[p.Fingerprint]})
		}
		b, _ := json.Marshal(out)
		fmt.Fprintf(stdout, "%s\n", b)
		return 0
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

func unpairOne(peers *loopback.Peers, prefix string, asJSON bool, stdout, stderr io.Writer) int {
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
	closed := revokeLinks([]loopback.Peer{p})
	if asJSON {
		return printUnpair(stdout, []loopback.Peer{p}, closed)
	}
	fmt.Fprintf(stdout, "forgot %s (%s)\n", loopback.ShortFingerprint(p.Fingerprint), p.Origin)
	printClosed(stderr, closed)
	return 0
}

func printUnpair(stdout io.Writer, gone []loopback.Peer, closed []string) int {
	out := unpairResult{Schema: unpairSchema, Removed: []string{}, Closed: closed}
	for _, p := range gone {
		out.Removed = append(out.Removed, p.Fingerprint)
	}
	b, _ := json.Marshal(out)
	fmt.Fprintf(stdout, "%s\n", b)
	return 0
}

func printClosed(stderr io.Writer, closed []string) {
	for _, fp := range closed {
		fmt.Fprintf(stderr, "slicc unpair: closed the live link of %s\n", loopback.ShortFingerprint(fp))
	}
}

func runningStatuses() []ctl.Entry {
	entries, _ := ctl.List(context.Background())
	return entries
}

func livePeers() map[string]bool {
	live := map[string]bool{}
	for _, e := range runningStatuses() {
		if e.Status == nil {
			continue
		}
		for _, fp := range e.Status.Peers {
			live[fp] = true
		}
	}
	return live
}

func revokeLinks(gone []loopback.Peer) []string {
	closed := []string{}
	if len(gone) == 0 {
		return closed
	}
	for _, e := range runningStatuses() {
		if e.Status == nil || !slices.Contains(e.Status.Ops, ctl.OpRevoke) {
			continue
		}
		for _, p := range gone {
			r, err := ctl.Call(context.Background(), e.Path, ctl.Request{Op: ctl.OpRevoke, Fingerprint: p.Fingerprint})
			if err == nil && r.OK && r.Closed && !slices.Contains(closed, p.Fingerprint) {
				closed = append(closed, p.Fingerprint)
			}
		}
	}
	return closed
}
