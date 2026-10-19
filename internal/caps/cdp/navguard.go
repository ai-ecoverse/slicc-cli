package cdp

import (
	"encoding/json"
	"time"
)

const (
	refusedGuard = "slicc can't keep seven's origin out of this target"
)

var slowGuard = 2 * time.Second

var navTypes = map[string]bool{"page": true, "iframe": true, "background_page": true, "webview": true}

type navTarget struct {
	session  string
	start    time.Time
	progress time.Time
	reason   string
	ready    chan struct{}
	pending  int
	done     bool
	failed   bool
}

func (nt *navTarget) settle(failed bool) {
	if nt.done {
		return
	}
	nt.done, nt.failed = true, failed
	close(nt.ready)
}

func (g *guard) documentPatterns() []any {
	var out []any
	for _, p := range g.o.DocumentPatterns() {
		out = append(out, map[string]any{"urlPattern": p, "resourceType": "Document", "requestStage": "Request"})
	}
	return out
}

func (g *guard) callbackLocked(sessionID, method string, params any, fn func(m message) [][]byte) []byte {
	msg, c := g.newCallLocked(sessionID, method, params, false)
	c.fn = fn
	return msg
}

func (g *guard) touchLocked(targetID, typ string) [][]byte {
	if targetID == "" || !navTypes[typ] || g.relaySession == "" {
		return nil
	}
	if _, ok := g.navTargets[targetID]; ok {
		return nil
	}
	nt := &navTarget{ready: make(chan struct{}), start: time.Now()}
	g.navTargets[targetID] = nt
	sent := time.Now()
	msg := g.callbackLocked(g.relaySession, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, func(m message) [][]byte {
		if took := time.Since(sent); took > slowGuard {
			g.logf("cdp: attaching a target's navigation guard took %s", took.Round(time.Millisecond))
		}
		return g.navAttachedLocked(nt, m)
	})
	return [][]byte{msg}
}

func (g *guard) navAttachedLocked(nt *navTarget, m message) [][]byte {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || res.SessionID == "" {
		g.logf("cdp: couldn't guard a target's navigations: %s", m.Error)
		nt.settle(true)
		return nil
	}
	nt.session = res.SessionID
	g.navSessions[res.SessionID] = nt
	sent := time.Now()
	step := func(method string) func(m message) [][]byte {
		return func(m message) [][]byte {
			if took := time.Since(sent); took > slowGuard {
				g.logf("cdp: %s for a target's navigation guard took %s", method, took.Round(time.Millisecond))
			}
			return nt.stepLocked(g, m)
		}
	}
	calls := []struct {
		method string
		params any
	}{
		{"Fetch.enable", map[string]any{"patterns": g.patterns}},
		{"Network.enable", map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0}},
		{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
	}
	nt.pending = len(calls)
	out := make([][]byte, 0, len(calls))
	for _, c := range calls {
		out = append(out, g.callbackLocked(res.SessionID, c.method, c.params, step(c.method)))
	}
	return out
}

func (nt *navTarget) stepLocked(g *guard, m message) [][]byte {
	if m.Error != nil {
		g.logf("cdp: couldn't guard a target's navigations: %s", m.Error)
		nt.settle(true)
		return nil
	}
	if nt.pending--; nt.pending == 0 {
		nt.settle(false)
	}
	return nil
}

func (g *guard) navEventLocked(nt *navTarget, m *message) [][]byte {
	if m.Method != "Fetch.requestPaused" {
		return nil
	}
	var p struct {
		RequestID    string `json:"requestId"`
		ResourceType string `json:"resourceType"`
		Request      struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.RequestID == "" {
		return nil
	}
	if p.ResourceType == "Document" && g.o.URL(p.Request.URL) {
		g.logf("cdp: failed a document request into a protected origin in a target this stream touched")
		msg, _ := g.newCallLocked(nt.session, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": "BlockedByClient"}, false)
		return [][]byte{msg}
	}
	msg, _ := g.newCallLocked(nt.session, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID}, false)
	return [][]byte{msg}
}

func (g *guard) navDetachedLocked(sessionID string) {
	nt, ok := g.navSessions[sessionID]
	if !ok {
		return
	}
	delete(g.navSessions, sessionID)
	if !nt.done {
		g.logf("cdp: lost the session guarding a target's navigations before it was ready")
	}
	nt.session = ""
	nt.settle(true)
	nt.failed = true
}

func (g *guard) navForgetLocked(targetID string) {
	nt, ok := g.navTargets[targetID]
	if !ok {
		return
	}
	delete(g.navTargets, targetID)
	if nt.session != "" {
		delete(g.navSessions, nt.session)
	}
	nt.settle(true)
}

func (g *guard) navWaitLocked(sessionID string) <-chan struct{} {
	s, ok := g.sessions[sessionID]
	if !ok || !navTypes[s.typ] {
		return nil
	}
	nt, ok := g.navTargets[s.target]
	if !ok || nt.done {
		return nil
	}
	return nt.ready
}

func (g *guard) navCheckLocked(sessionID string) string {
	s, ok := g.sessions[sessionID]
	if !ok || !navTypes[s.typ] {
		return ""
	}
	nt, ok := g.navTargets[s.target]
	switch {
	case ok && nt.done && !nt.failed:
		return ""
	case !ok:
		g.logf("cdp: refused a command on a target whose navigations aren't guarded")
	case !nt.done:
		g.logf("cdp: refused a command on a target whose navigation guard isn't ready")
	case nt.reason != "":
		return refusedGuard + ": " + nt.reason
	}
	return refusedGuard
}
