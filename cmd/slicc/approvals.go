package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const eventSchema = 1

const approvalsUsage = `usage: slicc <join-url> approvals [--watch] [--json] [--plain]
` + approvalsSynopsis + `
List what seven's approvals wait for a person to answer: one line per item,
with its id, kind, agent, the judge's verdict, the time left and a short
detail. Answer one with slicc decide.

flags:
  --watch            print a new list each time it changes, until Ctrl-C,
                     and reconnect when the link drops
  --json             print one {"schema":1,"event":"approvals",...} line per
                     list instead, and a reconnecting line before each new try
  --plain            no escape codes: with --watch on a terminal, print each
                     list after the last instead of redrawing it
` + watchFlags + `
Exit status: 0 after the list (or on Ctrl-C with --watch), 1 when seven
doesn't report its approvals or the link can't be set up, 2 for bad usage, 3
when seven doesn't allow remote control from this device, 130 on Ctrl-C.
`

type approvalsPrinter struct {
	f      agentFlags
	aio    agentIO
	redraw bool
	shown  bool
	now    func() time.Time
	status func(string, ...any)
}

func (p *approvalsPrinter) print(a agentctl.Approvals) {
	now := p.now()
	if p.f.json {
		_, _ = p.aio.stdout.Write(append(agentctl.ApprovalsLine(a, eventSchema, now), '\n'))
		return
	}
	switch {
	case p.redraw:
		_, _ = io.WriteString(p.aio.stdout, "\x1b[H\x1b[2J")
	case p.shown && len(a.Approvals) > 0:
		_, _ = io.WriteString(p.aio.stdout, "\n")
	}
	if len(a.Approvals) == 0 {
		p.status("nothing waits for an answer")
		return
	}
	p.shown = true
	agentctl.RenderApprovals(p.aio.stdout, a, now)
}

func (p *approvalsPrinter) reconnecting(attempt int, wait time.Duration) {
	if p.f.json {
		_, _ = p.aio.stdout.Write(append(agentctl.ReconnectingLine(eventSchema, attempt, p.now().Add(wait).UTC(), wait), '\n'))
	}
}

func (p *approvalsPrinter) unavailable(err error) {
	msg, ok := unavailableMessage(err)
	switch {
	case !ok:
	case p.f.json:
		b, _ := json.Marshal(struct {
			Schema    int    `json:"schema"`
			Event     string `json:"event"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		}{eventSchema, "error", msg, true})
		_, _ = p.aio.stdout.Write(append(b, '\n'))
	default:
		p.status("%s; trying again", agentctl.OneLineFull(msg))
	}
}

func approvalsRefused(aio agentIO, err error) (int, bool) {
	switch {
	case errors.Is(err, agentctl.ErrNoApprovals):
		return agentFail(aio, "approvals", err), true
	case errors.Is(err, agentctl.ErrNotAllowed):
		fmt.Fprintf(aio.stderr, "slicc approvals: %s\n", refusedMessage(err))
		return exitNoControl, true
	}
	return 0, false
}

func runApprovals(ctx context.Context, f agentFlags, aio agentIO) int {
	p := &approvalsPrinter{f: f, aio: aio, now: time.Now}
	p.redraw = f.watch && detectStyle(aio.stdout, f.plain).TTY
	p.status = func(format string, a ...any) { fmt.Fprintf(aio.stderr, "slicc: "+format+"\n", a...) }
	links, err := f.links(ctx, aio, p.status)
	if err != nil {
		return agentFail(aio, "approvals", err)
	}
	defer links.close()
	feed := &snapshotFeed{
		verb: "approvals", aio: aio, status: p.status,
		open: func(ctx context.Context, l agentctl.Link, watch bool) (snapshotStream, error) {
			st, err := agentctl.ListApprovals(ctx, l, watch)
			return shown(st, err, p.print)
		},
		refused:      func(err error) (int, bool) { return approvalsRefused(aio, err) },
		unavailable:  p.unavailable,
		reconnecting: p.reconnecting,
	}
	if !f.watch {
		return feed.once(ctx, links)
	}
	return feed.watch(ctx, links)
}
