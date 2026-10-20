//go:build !wasip1

package main

import (
	"strconv"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

func serveCtl(mode string, n *node.Node, kernelPort int, logf func(string, ...any)) (*ctl.Holder, func()) {
	h := ctl.NewHolder(mode, version)
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
