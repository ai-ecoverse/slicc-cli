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
	var ue *agentctl.UnavailableError
	if !errors.As(err, &ue) {
		return
	}
	if !p.f.json {
		p.status("%s; trying again", agentctl.OneLineFull(ue.Message))
		return
	}
	retryable := true
	b, _ := flow.Event{Event: flow.Error, Message: ue.Message, Retryable: &retryable}.MarshalJSON()
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
	if !f.watch {
		return agentsOnce(ctx, links, p)
	}
	return agentsWatch(ctx, links, p)
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

func agentsOnce(ctx context.Context, links agentLinks, p *inventoryPrinter) int {
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	defer cancel()
	l, err := links.next(sctx)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return setupFailed(ctx, p.aio, "agents", err)
	}
	st, err := agentctl.Inventory(sctx, l, false)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if code, done := agentsRefused(p.aio, err); done {
		return code
	}
	if err != nil {
		return setupFailed(ctx, p.aio, "agents", err)
	}
	_ = st.Close()
	p.print(st.First)
	return 0
}

type agentsWatcher struct {
	p       *inventoryPrinter
	backoff time.Duration
	opened  bool
	attempt int
}

func agentsWatch(ctx context.Context, links agentLinks, p *inventoryPrinter) int {
	w := &agentsWatcher{p: p, backoff: followBackoffBase}
	for {
		l, err := links.next(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			return agentFail(p.aio, "agents", err)
		}
		if !l.Allowed() {
			fmt.Fprintf(p.aio.stderr, "slicc agents: %s\n", noControlMessage)
			return exitNoControl
		}
		p.status("watching %s", l.Name())
		if code, stop := w.onLink(ctx, l); stop {
			return code
		}
		if !w.sleep(ctx) {
			return 0
		}
	}
}

func (w *agentsWatcher) onLink(ctx context.Context, l agentctl.Link) (int, bool) {
	for {
		err := w.once(ctx, l)
		if ctx.Err() != nil {
			return 0, true
		}
		if code, done := agentsRefused(w.p.aio, err); done {
			return code, true
		}
		w.p.unavailable(err)
		select {
		case <-l.Done():
			w.p.status("lost the link to %s; reconnecting", l.Name())
			return 0, false
		default:
		}
		if !w.sleep(ctx) {
			return 0, true
		}
	}
}

func (w *agentsWatcher) sleep(ctx context.Context) bool {
	wait := jitter(w.backoff)
	if !w.opened {
		w.backoff = min(w.backoff*2, followBackoffMax)
	}
	w.opened = false
	w.attempt++
	w.p.reconnecting(w.attempt, wait)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func (w *agentsWatcher) once(ctx context.Context, l agentctl.Link) error {
	st, err := agentctl.Inventory(ctx, l, true)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	w.opened = true
	w.backoff = followBackoffBase
	w.attempt = 0
	w.p.print(st.First)
	stop := context.AfterFunc(ctx, func() { _ = st.Close() })
	defer stop()
	for {
		s, err := st.Next()
		if err != nil {
			return err
		}
		w.p.print(s)
	}
}
