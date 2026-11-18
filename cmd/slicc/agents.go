//go:build !wasip1

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const agentsUsage = `usage: slicc <join-url> agents [--watch] [--json]
       slicc agents --cdp <url> [--origin <origin>]... [--watch] [--json]
       slicc agents --signal stdio [--watch] [--json]

List seven's agents: the cone its tab shows and that cone's live scoops, with
their status, queue and model. It's read-only, and nothing in it carries a
prompt, transcript text, a name, a path or a credential.

flags:
  --watch            print a new snapshot each time the agents change, until
                     Ctrl-C, and reconnect when the link drops
  --json             print one {"schema":1,"event":"inventory",...} line per
                     snapshot instead, and a reconnecting line before each
                     new try
` + watchFlags + `
Exit status: 0 after the snapshot (or on Ctrl-C with --watch), 1 when seven
doesn't report its agents or the link can't be set up, 2 for bad usage, 3
when seven doesn't allow remote control from this device, 130 on Ctrl-C.
`

type inventoryPrinter struct {
	f      agentFlags
	aio    agentIO
	tty    bool
	shown  int
	now    func() time.Time
	status func(string, ...any)
}

func (p *inventoryPrinter) print(s agentctl.Snapshot) {
	now := p.now()
	if p.f.json {
		_, _ = p.aio.stdout.Write(append(agentctl.InventoryLine(s, flow.Schema, now), '\n'))
		return
	}
	switch {
	case p.tty && p.f.watch:
		_, _ = io.WriteString(p.aio.stdout, "\x1b[H\x1b[2J")
	case p.shown > 0:
		_, _ = io.WriteString(p.aio.stdout, "\n")
	}
	p.shown++
	agentctl.RenderInventory(p.aio.stdout, s, now)
}

func (p *inventoryPrinter) reconnecting(attempt int, wait time.Duration) {
	if !p.f.json {
		return
	}
	at := p.now().Add(wait)
	e := reconnectingEvent(attempt, at, wait, "", "")
	b, _ := e.MarshalJSON()
	_, _ = p.aio.stdout.Write(append(b, '\n'))
}

var agentsLinks = func(ctx context.Context, f agentFlags, aio agentIO, status func(string, ...any)) (agentLinks, error) {
	return f.links(ctx, aio, status)
}

func (p *inventoryPrinter) unavailable(err error) {
	msg, ok := unavailableMessage(err)
	if !ok {
		return
	}
	if !p.f.json {
		p.status("%s; trying again", agentctl.OneLineFull(msg))
		return
	}
	retryable := true
	b, _ := flow.Event{Event: flow.Error, Message: msg, Retryable: &retryable}.MarshalJSON()
	_, _ = p.aio.stdout.Write(append(b, '\n'))
}

func agentsCommand(ctx context.Context, f agentFlags, aio agentIO) int {
	p := &inventoryPrinter{f: f, aio: aio, tty: detectStyle(aio.stdout, false).TTY, now: time.Now}
	p.status = func(format string, a ...any) { fmt.Fprintf(aio.stderr, "slicc: "+format+"\n", a...) }
	links, err := agentsLinks(ctx, f, aio, p.status)
	if err != nil {
		return agentFail(aio, "agents", err)
	}
	defer links.close()
	feed := &snapshotFeed{
		verb: "agents", aio: aio, status: p.status,
		open: func(ctx context.Context, l agentctl.Link, watch bool) (snapshotStream, error) {
			st, err := agentctl.Inventory(ctx, l, watch)
			return shown(st, err, p.print)
		},
		refused:      func(err error) (int, bool) { return agentsRefused(aio, err) },
		unavailable:  p.unavailable,
		reconnecting: p.reconnecting,
	}
	if !f.watch {
		return feed.once(ctx, links)
	}
	return feed.watch(ctx, links)
}

func agentsRefused(aio agentIO, err error) (int, bool) {
	switch {
	case errors.Is(err, agentctl.ErrNoInventory):
		return agentFail(aio, "agents", err), true
	case errors.Is(err, agentctl.ErrNotAllowed):
		fmt.Fprintf(aio.stderr, "slicc agents: %s\n", refusedMessage(err))
		return exitNoControl, true
	}
	return 0, false
}
