//go:build !wasip1

package main

import (
	"context"
	"os"
	ossignal "os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

type stopper struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	detached atomic.Bool
}

func (s *stopper) set(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
}

func (s *stopper) stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *stopper) detach() {
	s.detached.Store(true)
	s.stop()
}

func (s *stopper) kept() bool { return s != nil && s.detached.Load() }

func (s *stopper) register(h *ctl.Holder) {
	if s == nil {
		return
	}
	h.Handle(ctl.OpStop, s.stop)
	h.Handle(ctl.OpDetach, s.detach)
}

func signalContext(parent context.Context, st *stopper, keepOnTerm bool) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	st.set(cancel)
	ch := make(chan os.Signal, 1)
	ossignal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-ch:
			if keepOnTerm && sig == syscall.SIGTERM {
				st.detached.Store(true)
			}
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		ossignal.Stop(ch)
		cancel()
	}
}

func serveCtl(mode string, n *node.Node, kernelPort int, st *stopper, logf func(string, ...any)) (*ctl.Holder, func()) {
	h := ctl.NewHolder(mode, version)
	st.register(h)
	offers := nodeOffers(n.Hello(), kernelPort)
	h.Update(func(s *ctl.Status) { s.Offers = offers })
	srv, err := ctl.Serve(h)
	if err != nil {
		logf("control socket: %v", err)
		return h, func() {}
	}
	logf("control socket %s", srv.Path())
	return h, srv.Close
}

func watchLocal(h *ctl.Holder, n *node.Node, kernelPort int) func() {
	var mu sync.Mutex
	live := map[*link.Link]bool{}
	return n.Subscribe(func(e node.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case node.EventConnecting:
			if len(live) == 0 {
				h.SetState(ctl.StateConnecting)
			}
		case node.EventLinked:
			live[e.Link] = true
			name, origin, offers := e.Peer.Name, n.PeerOrigin(e.Link), nodeOffers(n.Hello(), kernelPort)
			h.Update(func(s *ctl.Status) {
				now := time.Now().UTC()
				s.State, s.Seven, s.Origin, s.Offers = ctl.StateConnected, name, origin, offers
				s.Links++
				s.LastLinked = &now
			})
		case node.EventLinkDown:
			delete(live, e.Link)
			if len(live) == 0 {
				h.SetState(ctl.StateWaiting)
			}
		}
	})
}

func nodeOffers(h link.Hello, kernelPort int) []string {
	out := []string{}
	if h.Routes != nil && h.Routes.Exit && h.HasCap("net") {
		out = append(out, "exit")
	}
	for _, c := range []string{"ssh", "cdp", "jerk"} {
		if h.HasCap(c) {
			out = append(out, c)
		}
	}
	if h.HasCap("fs") {
		for _, m := range h.Mounts {
			out = append(out, "fs:"+m.Name)
		}
		if len(h.Mounts) == 0 {
			out = append(out, "fs")
		}
	}
	if h.Kernel != nil {
		out = append(out, "kernel:"+strconv.Itoa(h.Kernel.Port))
	} else if kernelPort > 0 {
		out = append(out, "kernel:"+strconv.Itoa(kernelPort))
	}
	return out
}
