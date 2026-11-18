package cdp

import (
	"sort"
	"time"
)

const (
	refusedSession        = "slicc doesn't know which target this session belongs to"
	missingSessionMessage = "Session with given id not found"
)

func (g *guard) sessionKnownLocked(id string) bool {
	return id == "" || id == g.relaySession || g.sessions[id] != nil || g.held[id] != nil || g.hidden[id] || g.refused[id] || g.browserSessions[id] || g.navSessions[id] != nil
}

func (g *guard) sessionWait(sessionID string) <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionKnownLocked(sessionID) {
		return g.navWaitLocked(sessionID)
	}
	if g.gone[sessionID] {
		return nil
	}
	u := g.unknown[sessionID]
	if u == nil {
		u = &navTarget{ready: make(chan struct{}), start: time.Now()}
		u.awayAt = g.reads.away(u.start)
		g.unknown[sessionID] = u
		g.logf("cdp: holding commands on a session slicc doesn't know yet")
	}
	if u.done {
		return nil
	}
	return u.ready
}

func (g *guard) browserScope(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return sessionID == "" || g.browserSessions[sessionID]
}

func (g *guard) commandWait(c *command) <-chan struct{} {
	if g.startsUnheld(c) {
		return nil
	}
	if !g.browserScope(c.session) {
		return g.sessionWait(c.session)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	id := c.str("targetId")
	if ch := g.heldIframeWaitLocked(c); ch != nil {
		return ch
	}
	if exemptKeys[c.method]["targetId"] {
		return nil
	}
	nt := g.navTargets[id]
	if nt == nil || g.targetProtectedLocked(id) || nt.confirm == nil || nt.confirm.done {
		return nil
	}
	return nt.confirm.ready
}

func (g *guard) awaitCommand(c *command, ready, done <-chan struct{}) {
	for ready != nil {
		select {
		case <-ready:
			ready = g.commandWait(c)
		case <-done:
			return
		}
	}
}

func (g *guard) resolveUnknownLocked() {
	g.resolveHeldWaitsLocked()
	for id, u := range g.unknown {
		if g.sessionKnownLocked(id) {
			delete(g.unknown, id)
			u.settle(false)
		}
	}
}

func (g *guard) forgetUnknown(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u := g.unknown[sessionID]; u != nil && u.done {
		delete(g.unknown, sessionID)
	}
}

func (g *guard) unknownCheckLocked(sessionID string) string {
	if g.sessionKnownLocked(sessionID) {
		return ""
	}
	if g.gone[sessionID] {
		return missingSessionMessage
	}
	if u := g.unknown[sessionID]; u != nil {
		if !u.done {
			return ""
		}
		if u.reason != "" {
			return missingSessionMessage
		}
		return ""
	}
	return missingSessionMessage
}

func (g *guard) goneLocked(id string) {
	if id == "" || g.gone[id] {
		return
	}
	g.gone[id] = true
	g.goneOrder = append(g.goneOrder, id)
	if len(g.goneOrder) > maxRefused {
		delete(g.gone, g.goneOrder[0])
		g.goneOrder = g.goneOrder[1:]
	}
}

func (g *guard) sessionHasAncestorLocked(id, ancestor string) bool {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		if id == ancestor {
			return true
		}
		seen[id] = true
		if s := g.sessions[id]; s != nil {
			id = s.parent
			continue
		}
		if h := g.held[id]; h != nil {
			id = h.parent
			continue
		}
		return false
	}
	return false
}

func (g *guard) sessionParentDepthLocked(id string) int {
	depth := 0
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		depth++
		if s := g.sessions[id]; s != nil {
			id = s.parent
			continue
		}
		if h := g.held[id]; h != nil {
			id = h.parent
			continue
		}
		break
	}
	return depth
}

func (g *guard) takePendingMissingLocked(session string) [][]byte {
	if session == "" {
		return nil
	}
	var keys []callKey
	for k := range g.pending {
		if k.session == session {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].id < keys[j].id })
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		out = append(out, missingSession(k.id, k.session))
	}
	return out
}

func (g *guard) detachEventLocked(id, target string) []byte {
	params := map[string]any{"sessionId": id}
	if target != "" {
		params["targetId"] = target
	}
	return encode(map[string]any{"method": "Target.detachedFromTarget", "params": params})
}

func (g *guard) forgetOrphanLocked(id string, shown bool, target string) [][]byte {
	var page [][]byte
	if shown {
		page = append(page, g.detachEventLocked(id, target))
	}
	if h := g.held[id]; h != nil {
		delete(g.held, id)
		g.refuseHeldWaitLocked(h.target)
	}
	if g.hidden[id] {
		delete(g.hidden, id)
		delete(g.hiddenOwner, id)
	}
	page = append(page, g.takePendingMissingLocked(id)...)
	g.navDetachedLocked(id)
	if u := g.unknown[id]; u != nil {
		if u.reason == "" {
			u.reason = missingSessionMessage
		}
		u.settle(true)
		delete(g.unknown, id)
	}
	g.forgetSessionLocked(id)
	return page
}

func (g *guard) cascadeOrphansLocked(parent string) [][]byte {
	if parent == "" {
		return nil
	}
	type node struct {
		id     string
		depth  int
		shown  bool
		target string
	}
	var nodes []node
	seen := map[string]bool{}
	for id, s := range g.sessions {
		if id == parent || seen[id] || !g.sessionHasAncestorLocked(id, parent) {
			continue
		}
		seen[id] = true
		nodes = append(nodes, node{id: id, depth: g.sessionParentDepthLocked(id), shown: true, target: s.target})
	}
	for id, h := range g.held {
		if id == parent || seen[id] || !g.sessionHasAncestorLocked(id, parent) {
			continue
		}
		seen[id] = true
		nodes = append(nodes, node{id: id, depth: g.sessionParentDepthLocked(id), shown: false, target: h.target})
	}
	for id := range g.hidden {
		if id == parent || seen[id] || !g.sessionHasAncestorLocked(id, parent) {
			continue
		}
		seen[id] = true
		nodes = append(nodes, node{id: id, depth: g.sessionParentDepthLocked(id), shown: false})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].depth == nodes[j].depth {
			return nodes[i].id < nodes[j].id
		}
		return nodes[i].depth > nodes[j].depth
	})
	var page [][]byte
	for _, n := range nodes {
		page = append(page, g.forgetOrphanLocked(n.id, n.shown, n.target)...)
	}
	return page
}
