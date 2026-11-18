package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

type snapshotStream interface {
	show()
	next() error
	close()
}

type snapshotFeed struct {
	verb         string
	aio          agentIO
	status       func(string, ...any)
	open         func(ctx context.Context, l agentctl.Link, watch bool) (snapshotStream, error)
	refused      func(err error) (int, bool)
	unavailable  func(err error)
	reconnecting func(attempt int, wait time.Duration)
}

func (f *snapshotFeed) once(ctx context.Context, links agentLinks) int {
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	defer cancel()
	l, err := links.next(sctx)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return setupFailed(ctx, f.aio, f.verb, err)
	}
	st, err := f.open(sctx, l, false)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if code, done := f.refused(err); done {
		return code
	}
	if err != nil {
		return setupFailed(ctx, f.aio, f.verb, err)
	}
	st.close()
	st.show()
	return 0
}

func unavailableMessage(err error) (string, bool) {
	var ue *agentctl.UnavailableError
	if !errors.As(err, &ue) {
		return "", false
	}
	return ue.Message, true
}

type snapshotWatcher struct {
	f       *snapshotFeed
	backoff time.Duration
	opened  bool
	attempt int
}

func (f *snapshotFeed) watch(ctx context.Context, links agentLinks) int {
	w := &snapshotWatcher{f: f, backoff: followBackoffBase}
	for {
		l, err := links.next(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			return agentFail(f.aio, f.verb, err)
		}
		if !l.Allowed() {
			fmt.Fprintf(f.aio.stderr, "slicc %s: %s\n", f.verb, noControlMessage)
			return exitNoControl
		}
		f.status("watching %s", l.Name())
		if code, stop := w.onLink(ctx, l); stop {
			return code
		}
		if !w.sleep(ctx) {
			return 0
		}
	}
}

func (w *snapshotWatcher) onLink(ctx context.Context, l agentctl.Link) (int, bool) {
	for {
		err := w.once(ctx, l)
		if ctx.Err() != nil {
			return 0, true
		}
		if code, done := w.f.refused(err); done {
			return code, true
		}
		w.f.unavailable(err)
		select {
		case <-l.Done():
			w.f.status("lost the link to %s; reconnecting", l.Name())
			return 0, false
		default:
		}
		if !w.sleep(ctx) {
			return 0, true
		}
	}
}

func (w *snapshotWatcher) sleep(ctx context.Context) bool {
	wait := jitter(w.backoff)
	if !w.opened {
		w.backoff = min(w.backoff*2, followBackoffMax)
	}
	w.opened = false
	w.attempt++
	w.f.reconnecting(w.attempt, wait)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func (w *snapshotWatcher) once(ctx context.Context, l agentctl.Link) error {
	st, err := w.f.open(ctx, l, true)
	if err != nil {
		return err
	}
	defer st.close()
	w.opened = true
	w.backoff = followBackoffBase
	w.attempt = 0
	st.show()
	stop := context.AfterFunc(ctx, st.close)
	defer stop()
	for {
		if err := st.next(); err != nil {
			return err
		}
		st.show()
	}
}

type shownStream[T any] struct {
	st    *agentctl.SnapshotStream[T]
	cur   T
	print func(T)
}

func (s *shownStream[T]) show() { s.print(s.cur) }

func (s *shownStream[T]) next() error {
	v, err := s.st.Next()
	if err == nil {
		s.cur = v
	}
	return err
}

func (s *shownStream[T]) close() { _ = s.st.Close() }

func shown[T any](st *agentctl.SnapshotStream[T], err error, print func(T)) (snapshotStream, error) {
	if err != nil {
		return nil, err
	}
	return &shownStream[T]{st: st, cur: st.First, print: print}, nil
}
