//go:build !wasip1

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

const (
	modeCDP      = "cdp"
	modeLoopback = "loopback"
	modeTray     = "tray"
)

type jsonLines struct {
	mu     sync.Mutex
	w      io.Writer
	on     bool
	closed bool
}

func newJSONLines(w io.Writer, on bool) *jsonLines { return &jsonLines{w: w, on: on} }

func (j *jsonLines) emit(e flow.Event) {
	if j == nil || !j.on {
		return
	}
	b, _ := json.Marshal(e)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	fmt.Fprintf(j.w, "%s\n", b)
	if e.Event == flow.Closed {
		j.closed = true
	}
}

func (j *jsonLines) fail(msg, code string, retryable bool) {
	j.emit(flow.Event{Event: flow.Error, Message: msg, Code: code, Retryable: &retryable})
}

func (j *jsonLines) close() { j.emit(flow.Event{Event: flow.Closed}) }

func reconnectingEvent(attempt int, at time.Time, wait time.Duration, reason, code string) flow.Event {
	return flow.Event{Event: flow.Reconnecting, Attempt: attempt, RetryAt: &at, Wait: wait.Milliseconds(), Reason: reason, Code: code}
}

type resourceSource struct {
	mounts     []hostfs.Mount
	rejected   []hostfs.Rejected
	sftp       bool
	kernelPort int
	owned      map[string]bool
}

func (rs resourceSource) of(h link.Hello) *flow.Resources {
	r := &flow.Resources{
		Browsers:       []flow.ResourceBrowser{},
		Mounts:         []flow.ResourceMount{},
		RejectedMounts: []flow.RejectedMount{},
		Network:        flow.ResourceNetwork{Exit: h.Routes != nil && h.Routes.Exit && h.HasCap("net")},
		Compute:        flow.ResourceCompute{SSH: h.HasCap("ssh")},
		Jerk:           flow.ResourceJerk{Offered: h.HasCap("jerk"), Seal: h.HasCap("jerk-seal")},
		KernelPort:     rs.kernelPort,
	}
	r.Compute.SFTP = r.Compute.SSH && rs.sftp
	if h.Policy != nil {
		r.Network.Policy = h.Policy.Summary
	}
	if h.Kernel != nil {
		r.KernelPort = h.Kernel.Port
	}
	if h.HasCap("cdp") {
		for _, b := range h.Browsers {
			role := flow.RoleExtra
			if b.Seven {
				role = flow.RoleSeven
			}
			r.Browsers = append(r.Browsers, flow.ResourceBrowser{ID: b.ID, Name: b.Name, Role: role, Owned: rs.owned[b.ID]})
		}
	}
	if h.HasCap("fs") {
		paths := map[string]hostfs.Mount{}
		for _, m := range rs.mounts {
			paths[m.Name] = m
		}
		for _, m := range h.Mounts {
			r.Mounts = append(r.Mounts, flow.ResourceMount{Name: m.Name, Path: paths[m.Name].Root, ReadOnly: m.Readonly})
		}
	}
	for _, m := range rs.rejected {
		r.RejectedMounts = append(r.RejectedMounts, flow.RejectedMount{Path: m.Path, Reason: m.Reason})
	}
	return r
}

func (j *jsonLines) watchLinks(n *node.Node, mode string, res resourceSource) func() {
	if j == nil || !j.on {
		return func() {}
	}
	return n.Subscribe(func(e node.Event) { j.linkEvent(n, mode, res, e) })
}

func (j *jsonLines) linkEvent(n *node.Node, mode string, res resourceSource, e node.Event) {
	switch e.Kind {
	case node.EventConnecting:
		j.emit(flow.Event{Event: flow.Connecting, Mode: mode})
	case node.EventLinked:
		j.emit(flow.Event{Event: flow.Linked, Mode: mode, Peer: e.Peer.Name, Tab: n.PeerOrigin(e.Link), Caps: n.Caps(), Policy: policySummary(n), Resources: res.of(n.Hello())})
	case node.EventLinkDown:
		if e.Link == nil {
			if e.Err != nil {
				retry := true
				j.emit(flow.Event{Event: flow.Error, Mode: mode, Message: e.Err.Error(), Retryable: &retry})
			}
			return
		}
		ev := flow.Event{Event: flow.LinkLost, Mode: mode, Reason: e.Reason}
		if e.Err != nil && e.Reason == "" {
			ev.Message = e.Err.Error()
		}
		j.emit(ev)
	}
}

func policySummary(n *node.Node) string {
	if p := n.Hello().Policy; p != nil {
		return p.Summary
	}
	return ""
}
