package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const watchUsage = `usage: slicc <join-url> watch [--plain] [--json] [--thinking] [--since <entry-id>]
` + watchSynopsis + `
Follow seven's agent as it works, until Ctrl-C. Prompts and the assistant's
text go to stdout; tool calls, results, notices and errors go to stderr,
styled on a terminal and plain otherwise. When the stream or the link drops,
slicc reconnects with backoff and resumes after the last event it showed,
without repeating any.

flags:
  --thinking         also show the agent's thinking, on stderr
  --since <id>       start after this transcript entry (an event's id)
  --json             print the raw NDJSON events on stdout instead, after
                     slicc's own opened line for each stream, and its
                     reconnecting line before each new try
  --plain            no styling, even on a terminal
` + watchFlags + `
Exit status: ` + watchExit

type watcher struct {
	f       agentFlags
	aio     agentIO
	r       *agentctl.Renderer
	tracker *agentctl.Tracker
	backoff time.Duration
	opened  bool
	attempt int
}

func runWatch(ctx context.Context, f agentFlags, aio agentIO) int {
	w := &watcher{f: f, aio: aio, r: f.renderer(aio, true, true), tracker: agentctl.NewTracker(), backoff: followBackoffBase}
	w.tracker.Resume(f.since)
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
		if !l.Allowed() {
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

func (w *watcher) onLink(ctx context.Context, l agentctl.Link) (int, bool) {
	w.r.Status("watching %s", l.Name())
	for {
		err := w.once(ctx, l)
		if ctx.Err() != nil {
			return 0, true
		}
		if errors.Is(err, agentctl.ErrNotAllowed) {
			fmt.Fprintf(w.aio.stderr, "slicc watch: %s\n", refusedMessage(err))
			return exitNoControl, true
		}
		select {
		case <-l.Done():
			w.r.Status("lost the link to %s; reconnecting", l.Name())
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
	w.attempt++
	if w.f.json {
		w.line(agentLine{T: agentReconnecting, Attempt: w.attempt, Wait: wait.Milliseconds()})
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func (w *watcher) once(ctx context.Context, l agentctl.Link) error {
	st, err := agentctl.Watch(ctx, l, agentctl.WatchOptions{Since: w.tracker.Since(), Thinking: w.f.thinking})
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	w.opened = true
	w.backoff = followBackoffBase
	w.attempt = 0
	w.tracker.Reconnected()
	if w.f.json {
		w.line(agentLine{T: agentOpened, Head: &st.Head, Gap: st.Gap, Busy: st.Busy})
	}
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

type agentLine struct {
	T       string  `json:"t"`
	Head    *string `json:"head,omitempty"`
	Gap     bool    `json:"gap,omitempty"`
	Busy    *bool   `json:"busy,omitempty"`
	Attempt int     `json:"attempt,omitempty"`
	Wait    int64   `json:"wait,omitempty"`
}

func (w *watcher) line(l agentLine) {
	b, _ := json.Marshal(l)
	_, _ = w.aio.stdout.Write(append(b, '\n'))
}
