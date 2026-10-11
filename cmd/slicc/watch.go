package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const watchUsage = `usage: slicc <join-url> watch [--plain] [--json] [--thinking]
       slicc watch --cdp <url> [--origin <origin>]... [--plain] [--json] [--thinking]
       slicc watch --signal stdio [--thinking]

Follow seven's agent as it works, until Ctrl-C. Prompts and the assistant's
text go to stdout; tool calls, results, notices and errors go to stderr,
styled on a terminal and plain otherwise. When the stream or the link drops,
slicc reconnects with backoff and resumes after the last event it showed,
without repeating any.

flags:
  --thinking         also show the agent's thinking, on stderr
  --json             print the raw NDJSON events on stdout instead
  --plain            no styling, even on a terminal
  --cdp <url>        link over CDP instead of a join URL
  --origin <origin>  with --cdp, also accept seven on this origin
  --signal stdio     carry the local setup messages as NDJSON on stdin
                     and stdout instead of CDP (for a test harness);
                     stdout also carries the events, as with --json

Exit status: 0 on Ctrl-C, 1 when the link can't be set up for good, 2 for bad
usage, 3 when seven doesn't allow remote control from this device.
`

type watcher struct {
	f       agentFlags
	aio     agentIO
	r       *agentctl.Renderer
	tracker *agentctl.Tracker
	backoff time.Duration
	opened  bool
}

func runWatch(ctx context.Context, f agentFlags, aio agentIO) int {
	w := &watcher{f: f, aio: aio, r: f.renderer(aio, true, true), tracker: agentctl.NewTracker(), backoff: followBackoffBase}
	links, err := f.links(ctx, aio, w.r.Status)
	if err != nil {
		return agentFail(aio, "watch", err)
	}
	defer links.close()
	defer w.r.Finish()
	for {
		l, err := links.next(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			return agentFail(aio, "watch", err)
		}
		if !agentctl.Allowed(l) {
			fmt.Fprintf(aio.stderr, "slicc watch: %s\n", noControlMessage)
			return exitNoControl
		}
		if code, stop := w.onLink(ctx, l); stop {
			return code
		}
		if !w.sleep(ctx) {
			return 0
		}
	}
}

func (w *watcher) onLink(ctx context.Context, l *link.Link) (int, bool) {
	w.r.Status("watching %s", peerName(l))
	for {
		err := w.once(ctx, l)
		if ctx.Err() != nil {
			return 0, true
		}
		if errors.Is(err, agentctl.ErrNotAllowed) {
			fmt.Fprintf(w.aio.stderr, "slicc watch: %s\n", noControlMessage)
			return exitNoControl, true
		}
		select {
		case <-l.Done():
			w.r.Status("lost the link to %s; reconnecting", peerName(l))
			return 0, false
		default:
		}
		if !w.sleep(ctx) {
			return 0, true
		}
	}
}

func (w *watcher) sleep(ctx context.Context) bool {
	wait := jitter(w.backoff)
	if !w.opened {
		w.backoff = min(w.backoff*2, followBackoffMax)
	}
	w.opened = false
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func (w *watcher) once(ctx context.Context, l *link.Link) error {
	st, err := agentctl.Watch(ctx, l, agentctl.WatchOptions{Since: w.tracker.Since(), Thinking: w.f.thinking})
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	w.opened = true
	w.backoff = followBackoffBase
	w.tracker.Reconnected()
	if st.Gap {
		w.r.Notice("some events were missed")
	}
	stop := context.AfterFunc(ctx, func() { _ = st.Close() })
	defer stop()
	for {
		e, err := st.Next()
		if err != nil {
			return err
		}
		e, ok := w.tracker.Filter(e)
		if !ok {
			continue
		}
		if w.f.json {
			_, _ = w.aio.stdout.Write(append(e.Line, '\n'))
			continue
		}
		w.r.Render(e)
	}
}
