package cdp

import "time"

const refusedSession = "slicc doesn't know which target this session belongs to"

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

func (g *guard) awaitSession(sessionID string, ready, done <-chan struct{}) {
	for ready != nil {
		select {
		case <-ready:
			ready = g.sessionWait(sessionID)
		case <-done:
			return
		}
	}
}

func (g *guard) resolveUnknownLocked() {
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
	if u := g.unknown[sessionID]; u != nil && u.reason != "" {
		return refusedSession + ": " + u.reason
	}
	return refusedSession
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
