//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

const trayNotRunning = "not running"

const trayUsage = `usage: slicc tray [--json] [-q] [-a]

The slicc processes on this machine that hold a link to seven: follow
(remote, through the tray hub), and attach, open, pair and the default flow
(local). Each one serves a control socket in a directory only you can use;
slicc tray asks each one for its state, and removes the sockets of
processes that are gone. The join URL is never printed.

  --json   print them as a JSON array
  -q       print only their PIDs, for slicc tray -q | xargs kill
  -a       also list the trays this machine follows but isn't running now

Inside seven, slicc tray shows that seven's own tray instead.
`

func runTray(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tray", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "")
	quiet := fs.Bool("q", false, "")
	all := fs.Bool("a", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, trayUsage)
			return 0
		}
		fmt.Fprintf(stderr, "slicc tray: %v\n\n%s", err, trayUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc tray: unexpected argument %q\n\n%s", fs.Arg(0), trayUsage)
		return 2
	}
	if *quiet && *asJSON {
		fmt.Fprintf(stderr, "slicc tray: -q and --json don't go together\n\n%s", trayUsage)
		return 2
	}
	entries, err := ctl.List(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "slicc tray: %v\n", err)
		return 1
	}
	var rows []ctl.Status
	for _, e := range entries {
		switch {
		case e.Status != nil:
			rows = append(rows, *e.Status)
		case errors.Is(e.Err, ctl.ErrStale):
			if gone, _ := ctl.Wipe(e.Path); gone {
				fmt.Fprintf(stderr, "slicc tray: removed the stale socket of PID %d\n", e.PID)
			}
		default:
			rows = append(rows, ctl.Status{V: 1, PID: e.PID, State: "not answering", Error: e.Err.Error(), Offers: []string{}})
		}
	}
	if *quiet {
		for _, r := range rows {
			fmt.Fprintln(stdout, r.PID)
		}
		return 0
	}
	if *all {
		rows = append(rows, knownTrays(rows)...)
	}
	for i := range rows {
		scrubRow(&rows[i])
	}
	if *asJSON {
		if rows == nil {
			rows = []ctl.Status{}
		}
		body, _ := json.Marshal(rows)
		fmt.Fprintf(stdout, "%s\n", body)
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(stderr, "slicc tray: no slicc on this machine holds a link")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PID\tMODE\tSEVEN\tDEVICE\tSTATE\tOFFERS")
	now := time.Now()
	for _, r := range rows {
		pid := "-"
		if r.PID > 0 {
			pid = strconv.Itoa(r.PID)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", pid, dash(r.Mode), sevenName(r), shortID(r.Device), stateText(r, now), dash(strings.Join(r.Offers, ",")))
	}
	_ = tw.Flush()
	return 0
}

var joinURLs = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]*/join/[A-Za-z0-9._~%+-]+`)

func scrub(s string) string {
	s = strings.Join(strings.Fields(agentctl.Clean(s)), " ")
	return joinURLs.ReplaceAllString(s, "<join-url>")
}

func scrubRow(r *ctl.Status) {
	for _, f := range []*string{&r.Mode, &r.Version, &r.Seven, &r.Origin, &r.Hub, &r.Device, &r.State, &r.Code, &r.Error} {
		*f = scrub(*f)
	}
	for i := range r.Offers {
		r.Offers[i] = scrub(r.Offers[i])
	}
}

func knownTrays(running []ctl.Status) []ctl.Status {
	live := map[string]bool{}
	for _, r := range running {
		if r.Mode == "follow" {
			live[r.Hub] = true
		}
	}
	var out []ctl.Status
	seen := map[string]bool{}
	add := func(hub, leader, fp string) {
		if live[hub] || seen[hub] {
			return
		}
		seen[hub] = true
		out = append(out, ctl.Status{V: 1, Mode: "follow", Hub: hub, Seven: leader, Device: fp, State: trayNotRunning, Offers: []string{}})
	}
	if certs, err := tray.OpenDefaultCerts(); err == nil {
		for _, k := range certs.Known() {
			add(k.Origin, k.Leader, k.Fingerprint)
		}
	}
	if state, err := tray.OpenDefaultState(); err == nil {
		for _, hub := range state.Hubs() {
			add(hub, "", "")
		}
	}
	return out
}

func sevenName(r ctl.Status) string {
	switch {
	case r.Seven != "":
		return r.Seven
	case r.Origin != "":
		return hostOf(r.Origin)
	case r.Hub != "":
		return "(" + hostOf(r.Hub) + ")"
	}
	return "-"
}

func hostOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	if p := u.Port(); (u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80") {
		return u.Hostname()
	}
	return u.Host
}

func stateText(r ctl.Status, now time.Time) string {
	switch r.State {
	case ctl.StateConnected:
		return r.State + " " + compact(now.Sub(r.Since))
	case ctl.StateReconnecting:
		wait := time.Duration(0)
		if r.RetryAt != nil {
			wait = max(r.RetryAt.Sub(now), 0)
		}
		return fmt.Sprintf("reconnecting (#%d, %s)", r.Attempt, compact(wait))
	case ctl.StateStopped:
		if r.Code != "" {
			return "stopped (" + r.Code + ")"
		}
	}
	return dash(r.State)
}

func compact(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Round(time.Second)/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

func shortID(id string) string {
	if len(id) <= 12 {
		return dash(id)
	}
	return id[:4] + "…" + id[len(id)-4:]
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
