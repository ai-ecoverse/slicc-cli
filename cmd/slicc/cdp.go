//go:build !wasip1

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/cdpin"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

const cdpUsage = `usage: slicc <join-url> cdp [--port <n>] [--json]
       slicc - cdp [flags]
       slicc @<file> cdp [flags]
       slicc cdp --cdp <url> [--origin <origin>]... [flags]
       slicc cdp --signal stdio [flags]

Serve seven's browser as a CDP endpoint on this machine, for Playwright,
Puppeteer and other CDP clients. With a join URL, slicc links to that seven
through the tray hub as this device, as prompt does; seven must allow
Browser for this device (Linked devices). With --cdp, slicc links to the
seven tab in that CDP browser, as slicc attach does. The link offers nothing
to seven, and everything goes through seven's guard: seven's own pages and
the vault stay out of reach.

The endpoint listens on 127.0.0.1 (and [::1]) only, takes connections from
this machine only, and refuses requests from web pages (Host and Origin
checks, as Chrome does). There's no token: any program on this machine can
use it while it runs.

flags:
  --port <n>         listen on this port (default 9333; 0 picks a free one)
  --json             print NDJSON lifecycle events on stdout
  --cdp <url>        link over CDP instead of a join URL
  --origin <origin>  with --cdp, also accept seven on this origin
  --signal stdio     carry the local setup messages as NDJSON on stdin and
                     stdout instead of CDP (for a test harness)

Exit status: 0 on Ctrl-C, SIGTERM or slicc tray's stop, 1 when the port is
taken, seven can't share its browser or the link can't be set up for good,
2 for bad usage, 3 when seven doesn't allow browser control from this
device.
`

const cdpReadyMargin = 500 * time.Millisecond

const (
	cdpNoCap   = "this seven can't share its browser yet; update it"
	cdpServing = "serving seven's browser at %s (%s)"
)

type cdpFlags struct {
	joinURL string
	port    int
	json    bool
	cdp     string
	signal  string
	origins []string
}

type rawLinks interface {
	nextLink(ctx context.Context) (*link.Link, error)
	close()
}

func parseCDP(joinURL string, args []string, aio agentIO) (cdpFlags, int) {
	f := cdpFlags{joinURL: joinURL, port: cdpin.DefaultPort, signal: "cdp"}
	fs := flag.NewFlagSet("cdp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&f.port, "port", cdpin.DefaultPort, "")
	fs.BoolVar(&f.json, "json", false, "")
	if joinURL == "" {
		fs.StringVar(&f.cdp, "cdp", "", "")
		fs.Var((*multi)(&f.origins), "origin", "")
		fs.StringVar(&f.signal, "signal", "cdp", "")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(aio.stdout, cdpUsage)
			return f, 0
		}
		fmt.Fprintf(aio.stderr, "slicc cdp: %v\n\n%s", err, cdpUsage)
		return f, 2
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(aio.stderr, "slicc cdp: unexpected arguments\n\n%s", cdpUsage)
		return f, 2
	case f.port < 0 || f.port > 65535:
		fmt.Fprintf(aio.stderr, "slicc cdp: --port needs a port from 0 to 65535\n")
		return f, 2
	case f.signal != "cdp" && f.signal != "stdio":
		fmt.Fprintf(aio.stderr, "slicc cdp: --signal must be cdp or stdio\n")
		return f, 2
	case joinURL == "" && f.cdp == "" && f.signal != "stdio":
		fmt.Fprintf(aio.stderr, "slicc cdp: give a join URL (slicc <join-url> cdp) or --cdp <url>\n\n%s", cdpUsage)
		return f, 2
	}
	return f, -1
}

type cdpRun struct {
	f      cdpFlags
	aio    agentIO
	out    *jsonLines
	srv    *cdpin.Server
	ctl    *ctl.Holder
	mode   string
	status func(string, ...any)
	cur    chan *link.Link
}

func (r *cdpRun) current() *link.Link {
	l := <-r.cur
	r.cur <- l
	return l
}

func (r *cdpRun) set(l *link.Link) {
	<-r.cur
	r.cur <- l
}

func runCDP(joinURL string, args []string, aio agentIO) int {
	f, code := parseCDP(joinURL, args, aio)
	if code >= 0 {
		return code
	}
	if f.signal == "stdio" {
		aio.stdout = &lineWriter{w: aio.stdout}
	}
	st := &stopper{}
	ctx, stop := signalContext(context.Background(), st, false)
	defer stop()
	r := &cdpRun{f: f, aio: aio, out: newJSONLines(aio.stdout, f.json), mode: modeCDP, cur: make(chan *link.Link, 1)}
	r.cur <- nil
	r.status = func(format string, a ...any) { fmt.Fprintf(aio.stderr, "slicc: "+format+"\n", a...) }
	switch {
	case joinURL != "":
		r.mode = modeTray
	case f.signal == "stdio":
		r.mode = "stdio"
	}
	defer r.out.end(false)
	return r.run(ctx, st)
}

func (r *cdpRun) fail(code int, msg string) int {
	fmt.Fprintf(r.aio.stderr, "slicc cdp: %s\n", msg)
	r.out.fail(msg, "", false)
	return code
}

func (r *cdpRun) run(ctx context.Context, st *stopper) int {
	logf := agentLogf(r.aio.stderr)
	srv, err := cdpin.Listen(cdpin.Config{Port: r.f.port, Link: r.current, Clients: r.clients, Logf: logf})
	if err != nil {
		return r.fail(1, err.Error())
	}
	r.srv = srv
	r.ctl = ctl.NewHolder("cdp", version)
	r.ctl.Handle(ctl.OpStop, st.stop)
	r.ctl.Update(func(s *ctl.Status) {
		s.Offers = []string{"cdp:" + strconv.Itoa(srv.Port())}
		s.CDP = &ctl.CDPStatus{Port: srv.Port()}
	})
	cs, err := ctl.Serve(r.ctl)
	if err != nil {
		srv.Close()
		return r.fail(1, fmt.Sprintf("can't serve the control socket (%v), so this machine's other slicc processes couldn't keep port %d from seven; not serving", err, srv.Port()))
	}
	defer cs.Close()
	visible := time.Now().Add(browser.ServedLag() + cdpReadyMargin)
	af := agentFlags{verb: "cdp", joinURL: r.f.joinURL, cdp: r.f.cdp, signal: r.f.signal, origins: r.f.origins}
	al, err := af.links(ctx, r.aio, r.status)
	if err != nil {
		srv.Close()
		return r.fail(1, err.Error())
	}
	links := al.(rawLinks)
	defer func() {
		srv.Close()
		links.close()
	}()
	if rl, ok := links.(*remoteLinks); ok {
		r.ctl.Update(func(s *ctl.Status) {
			s.Hub, s.Device, s.State = tray.HubOrigin(r.f.joinURL), rl.device, ctl.StateDialing
		})
		rl.retry = r.retry
	}
	r.out.emit(flow.Event{Event: flow.Connecting, Mode: r.mode})
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	l, err := links.nextLink(sctx)
	cancel()
	if ctx.Err() != nil {
		return 0
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no seven answered within %s", agentSetupTimeout)
		}
		return r.fail(1, err.Error())
	}
	if code := r.first(ctx, l); code >= 0 {
		return code
	}
	ready := visible
	if now := time.Now(); now.After(ready) {
		ready = now
	}
	srv.Serve(ready)
	select {
	case <-ctx.Done():
		return 0
	case <-time.After(time.Until(ready)):
	}
	r.status(cdpServing, srv.URL(), peerName(l))
	r.out.emit(flow.Event{Event: flow.Listening, URL: srv.URL()})
	return r.serve(ctx, links, l)
}

func (r *cdpRun) first(ctx context.Context, l *link.Link) int {
	h, _ := l.PeerHello()
	if !h.HasCap(cdpin.Cap) {
		return r.fail(1, cdpNoCap)
	}
	pctx, cancel := context.WithTimeout(ctx, cdpin.OpenTimeout)
	_, err := cdpin.Probe(pctx, l)
	cancel()
	if ctx.Err() != nil {
		return 0
	}
	var rf *cdpin.Refused
	switch {
	case err == nil:
	case errors.As(err, &rf) && rf.Code == "EACCES":
		return r.fail(exitNoControl, rf.Error())
	case errors.As(err, &rf) && rf.Code == "EOPNOTSUPP":
		return r.fail(1, cdpNoCap)
	default:
		return r.fail(1, err.Error())
	}
	r.linked(l)
	return -1
}

func (r *cdpRun) linked(l *link.Link) {
	r.set(l)
	name := peerName(l)
	r.ctl.Update(func(s *ctl.Status) {
		now := time.Now().UTC()
		s.State, s.Seven, s.Attempt, s.RetryAt, s.Error = ctl.StateConnected, name, 0, nil, ""
		s.Links++
		s.LastLinked = &now
	})
	r.out.emit(flow.Event{Event: flow.Linked, Mode: r.mode, Peer: name})
}

func (r *cdpRun) retry(attempt int, wait time.Duration, reason string) {
	at := time.Now().Add(wait).UTC()
	r.ctl.Update(func(s *ctl.Status) {
		s.State, s.Attempt, s.RetryAt, s.Error = ctl.StateReconnecting, attempt, &at, reason
	})
	r.out.emit(reconnectingEvent(attempt, at, wait, reason, ""))
}

func (r *cdpRun) clients(n int) {
	if r.ctl != nil {
		r.ctl.Update(func(s *ctl.Status) {
			if s.CDP != nil {
				s.CDP.Clients = n
			}
		})
	}
	r.out.emit(flow.Event{Event: flow.Clients, Count: n})
}

func (r *cdpRun) serve(ctx context.Context, links rawLinks, l *link.Link) int {
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-l.Done():
		}
		r.set(nil)
		r.status("lost the link to seven; reconnecting")
		r.out.emit(flow.Event{Event: flow.LinkLost, Mode: r.mode})
		r.ctl.SetState(ctl.StateReconnecting)
		for {
			next, err := links.nextLink(ctx)
			if ctx.Err() != nil {
				return 0
			}
			if err != nil {
				var fe *fatalError
				if errors.As(err, &fe) {
					return r.fail(1, err.Error())
				}
				continue
			}
			h, _ := next.PeerHello()
			if !h.HasCap(cdpin.Cap) {
				r.status("%s", cdpNoCap)
			}
			l = next
			break
		}
		r.linked(l)
		r.status("linked again with %s", strings.TrimSpace(peerName(l)))
	}
}
