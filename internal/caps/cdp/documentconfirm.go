package cdp

import (
	"encoding/json"
	"strings"
	"time"
)

type docHold struct {
	parent   string
	typ      string
	waiting  bool
	event    []byte
	reply    []byte
	replyID  int64
	replySid string
	hasEvent bool
	hasReply bool
}

type docConfirm struct {
	target   string
	holds    map[string]*docHold
	ready    chan struct{}
	start    time.Time
	deadline time.Time
	timer    *time.Timer
	asking   bool
	done     bool
	failed   bool
}

func (g *guard) docConfirmWaitLocked(session string) <-chan struct{} {
	if session == "" {
		return nil
	}
	for _, dc := range g.docInflight {
		if dc == nil || dc.done || dc.holds[session] == nil {
			continue
		}
		return dc.ready
	}
	return nil
}

func (g *guard) needsDocConfirmLocked(target, session, typ string) bool {
	if target == "" || typ == "browser" {
		return false
	}
	if g.docPassed[session] {
		return false
	}
	if g.docFailed[session] {
		return true
	}
	if dc := g.docInflight[target]; dc != nil && !dc.done {
		return true
	}
	if g.explicit[target] > 0 {
		return true
	}
	for _, t := range g.attachCalls {
		if t == target {
			return true
		}
	}
	return false
}

func (g *guard) docConfirmSkipChromeErrorLocked(url, target string) bool {
	if chromeErrorDocument(url) {
		return true
	}
	if t := g.targets[target]; t != nil && chromeErrorDocument(t.url) {
		return true
	}
	if g.failedLoadNeedsProofLocked(g.targets[target]) || g.errorProofOfLocked(target) != errorProofNone {
		return true
	}
	if nt := g.navTargets[target]; nt != nil && nt.done && nt.errorURL != "" && chromeErrorDocument(nt.documentURL) {
		return true
	}
	return false
}

func (g *guard) docConfirmLiveProtectedDocumentLocked(url string) bool {
	if url == "" || chromeErrorDocument(url) || g.o == nil || !g.o.Loaded(url) {
		return false
	}
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:")
}

func (g *guard) docConfirmRefuseLocked(id string, info targetInfo) bool {
	if g.workerConcealedLocked(id) || g.iframeConcealedLocked(id) {
		return true
	}
	if g.targetProtectedLocked(id) {
		return true
	}
	if g.targetUnconfirmedLocked(id) {
		return g.docConfirmLiveProtectedDocumentLocked(info.URL)
	}
	return g.targetReasonLocked(id) != ""
}

func (g *guard) beginDocConfirmLocked(target, session, parent, typ string, waiting bool) *docConfirm {
	if g.docInflight == nil {
		g.docInflight = map[string]*docConfirm{}
	}
	dc := g.docInflight[target]
	if dc == nil || dc.done {
		now := time.Now()
		dc = &docConfirm{
			target:   target,
			holds:    map[string]*docHold{},
			ready:    make(chan struct{}),
			start:    now,
			deadline: now.Add(holdTimeout()),
		}
		g.docInflight[target] = dc
		g.armDocConfirmTimerLocked(dc)
	}
	if dc.holds[session] == nil {
		dc.holds[session] = &docHold{parent: parent, typ: typ, waiting: waiting}
	} else {
		h := dc.holds[session]
		if parent != "" {
			h.parent = parent
		}
		if typ != "" {
			h.typ = typ
		}
		h.waiting = h.waiting || waiting
	}
	return dc
}

func (g *guard) armDocConfirmTimerLocked(dc *docConfirm) {
	if dc == nil || dc.done {
		return
	}
	g.stopDocTimerLocked(dc)
	wait := time.Until(dc.deadline)
	if wait < 0 {
		wait = 0
	}
	target := dc.target
	dc.timer = time.AfterFunc(wait, func() {
		g.expireDocConfirm(target)
	})
}

func (g *guard) expireDocConfirm(target string) {
	g.mu.Lock()
	dc := g.docInflight[target]
	if dc == nil || dc.done {
		g.mu.Unlock()
		return
	}
	if rem := time.Until(dc.deadline); rem > 0 {
		g.armDocConfirmTimerLocked(dc)
		g.mu.Unlock()
		return
	}
	g.logf("cdp: confirming a target's current document hit the hold bound")
	send := g.failDocConfirmLocked(dc)
	page := g.takePendingPageLocked()
	flush := g.flushAsync
	g.mu.Unlock()
	if flush != nil {
		flush(send, page)
	}
}

func (g *guard) askDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc == nil || dc.asking || dc.done {
		return nil
	}
	if g.relaySession == "" {
		return g.failDocConfirmLocked(dc)
	}
	dc.asking = true
	dc.start = time.Now()
	target := dc.target
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
		return g.answeredDocConfirmLocked(target, m)
	})}
}

func (g *guard) answeredDocConfirmLocked(target string, m message) [][]byte {
	dc := g.docInflight[target]
	if dc == nil || dc.done {
		return nil
	}
	g.logf("cdp: confirming a target's current document took %s", time.Since(dc.start).Round(time.Millisecond))
	dc.asking = false
	var result struct {
		TargetInfo targetInfo `json:"targetInfo"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.TargetInfo.TargetID == "" || result.TargetInfo.TargetID != dc.target {
		return g.failDocConfirmLocked(dc)
	}
	if result.TargetInfo.Type == "" {
		for _, h := range dc.holds {
			if h.typ != "" {
				result.TargetInfo.Type = h.typ
				break
			}
		}
	}
	send := g.observeLocked(result.TargetInfo)
	if g.docConfirmRefuseLocked(dc.target, result.TargetInfo) {
		return append(send, g.failDocConfirmLocked(dc)...)
	}
	return append(send, g.passDocConfirmLocked(dc)...)
}

func (g *guard) stopDocTimerLocked(dc *docConfirm) {
	if dc != nil && dc.timer != nil {
		dc.timer.Stop()
		dc.timer = nil
	}
}

func (g *guard) cancelDocConfirmLocked(dc *docConfirm) {
	if dc == nil || dc.done {
		return
	}
	dc.done = true
	g.stopDocTimerLocked(dc)
	close(dc.ready)
	delete(g.docInflight, dc.target)
}

func (g *guard) clearDocFailedSessionLocked(session string) {
	if session == "" {
		return
	}
	delete(g.docFailed, session)
	delete(g.hidden, session)
	delete(g.hiddenOwner, session)
}

func (g *guard) scheduleDocFailedClearLocked(sessions []string) {
	if len(sessions) == 0 {
		return
	}
	copied := append([]string(nil), sessions...)
	time.AfterFunc(holdTimeout(), func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, sid := range copied {
			if g.docFailed[sid] {
				g.clearDocFailedSessionLocked(sid)
			}
		}
	})
}

func (g *guard) failDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc == nil || dc.done {
		return nil
	}
	dc.done = true
	dc.failed = true
	g.stopDocTimerLocked(dc)
	close(dc.ready)
	delete(g.docInflight, dc.target)
	if g.docFailed == nil {
		g.docFailed = map[string]bool{}
	}
	var send [][]byte
	var failed []string
	for sid, h := range dc.holds {
		g.docFailed[sid] = true
		failed = append(failed, sid)
		g.noteHiddenLocked(sid, dc.target)
		if h.waiting {
			send = append(send, g.resumeLocked(sid, dc.target))
		}
		parent := h.parent
		if parent == "" {
			parent = g.attachParent
		}
		msg, _ := g.newCallLocked(parent, "Target.detachFromTarget", map[string]any{"sessionId": sid}, false)
		send = append(send, msg)
		if h.hasReply {
			g.pendingPage = append(g.pendingPage, refusal(h.replyID, h.replySid, refusedTarget))
		}
	}
	g.scheduleDocFailedClearLocked(failed)
	g.logf("cdp: refused an attach after confirming the target's current document")
	return send
}

func (g *guard) passDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc == nil || dc.done {
		return nil
	}
	dc.done = true
	dc.failed = false
	g.stopDocTimerLocked(dc)
	close(dc.ready)
	delete(g.docInflight, dc.target)
	if g.docPassed == nil {
		g.docPassed = map[string]bool{}
	}
	var send [][]byte
	for sid, h := range dc.holds {
		g.docPassed[sid] = true
		typ := h.typ
		if typ == "" {
			typ = "page"
		}
		if t := g.targets[dc.target]; t != nil {
			typ = t.typ
			if workerTypes[t.typ] || t.typ == "iframe" {
				t.shown = true
			}
			g.markTouchedLocked(dc.target)
		}
		if g.sessions[sid] == nil {
			g.sessions[sid] = &session{id: sid, target: dc.target, parent: h.parent, typ: typ, waiting: h.waiting}
			if typ == "browser" {
				g.browserSessions[sid] = true
			}
		}
		send = append(send, g.touchLocked(dc.target, typ)...)
		if typ == "page" {
			send = append(send, g.scheduleDocConfirmObserveLocked(dc.target, sid, g.docConfirmObservePathLocked(dc.target, typ))...)
		}
		if h.hasEvent && len(h.event) > 0 {
			g.pendingPage = append(g.pendingPage, h.event)
		}
		if h.hasReply && len(h.reply) > 0 {
			g.pendingPage = append(g.pendingPage, h.reply)
		}
	}
	return send
}

func (g *guard) pruneDocConfirmSessionLocked(session string) {
	if session == "" {
		return
	}
	delete(g.docPassed, session)
	g.clearDocFailedSessionLocked(session)
	for _, dc := range g.docInflight {
		if dc == nil || dc.holds[session] == nil {
			continue
		}
		delete(dc.holds, session)
		if len(dc.holds) == 0 && !dc.done {
			g.cancelDocConfirmLocked(dc)
		}
	}
}

func (g *guard) pruneDocConfirmTargetLocked(target string) {
	if target == "" {
		return
	}
	if dc := g.docInflight[target]; dc != nil && !dc.done {
		g.failDocConfirmLocked(dc)
	}
	for sid := range g.docFailed {
		if g.hiddenOwner[sid] == target {
			g.clearDocFailedSessionLocked(sid)
		}
	}
	for sid := range g.docPassed {
		if s := g.sessions[sid]; s != nil && s.target == target {
			delete(g.docPassed, sid)
		}
	}
}

func (g *guard) docConfirmAttachEventLocked(m *message, data []byte, session string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	if g.docFailed[session] {
		return browserVerdict{send: send}, true
	}
	if g.docPassed[session] {
		return browserVerdict{}, false
	}
	if !g.needsDocConfirmLocked(info.TargetID, session, info.Type) {
		return browserVerdict{}, false
	}
	if g.docConfirmSkipChromeErrorLocked(info.URL, info.TargetID) {
		return browserVerdict{}, false
	}
	dc := g.beginDocConfirmLocked(info.TargetID, session, m.SessionID, info.Type, waiting)
	h := dc.holds[session]
	h.hasEvent = true
	if params := decodeParams(m.Params); params != nil {
		if obj, _ := params["targetInfo"].(map[string]any); g.redactInfoLocked(obj, info) {
			m.Params = encode(params)
			h.event = encode(m)
		} else {
			h.event = append([]byte(nil), data...)
		}
	} else {
		h.event = append([]byte(nil), data...)
	}
	ask := g.askDocConfirmLocked(dc)
	return browserVerdict{send: append(send, ask...), page: g.takePendingPageLocked()}, true
}

func (g *guard) holdDocConfirmReplyLocked(attached string, m *message, res map[string]any) (held bool, send [][]byte, page [][]byte) {
	sid, _ := res["sessionId"].(string)
	if sid == "" || sid == g.relaySession {
		return false, nil, nil
	}
	if g.docFailed[sid] {
		return true, nil, [][]byte{refusal(*m.ID, m.SessionID, refusedTarget)}
	}
	if g.docPassed[sid] {
		return false, nil, nil
	}
	typ := ""
	if t := g.targets[attached]; t != nil {
		typ = t.typ
	}
	if typ == "browser" {
		return false, nil, nil
	}
	if typ == "" {
		typ = "page"
	}
	if g.docConfirmSkipChromeErrorLocked("", attached) {
		return false, nil, nil
	}
	if dc := g.docInflight[attached]; dc != nil && !dc.done {
		h := dc.holds[sid]
		if h == nil {
			h = &docHold{parent: m.SessionID, typ: typ}
			dc.holds[sid] = h
		}
		h.hasReply = true
		h.replyID = *m.ID
		h.replySid = m.SessionID
		wrapped := map[string]any{"id": *m.ID, "result": res}
		if m.SessionID != "" {
			wrapped["sessionId"] = m.SessionID
		}
		h.reply = encode(wrapped)
		return true, g.askDocConfirmLocked(dc), g.takePendingPageLocked()
	}
	dc := g.beginDocConfirmLocked(attached, sid, m.SessionID, typ, false)
	if dc.done {
		if dc.failed || g.docFailed[sid] {
			return true, nil, [][]byte{refusal(*m.ID, m.SessionID, refusedTarget)}
		}
		if g.docPassed[sid] {
			return false, nil, nil
		}
		return false, nil, nil
	}
	h := dc.holds[sid]
	h.hasReply = true
	h.replyID = *m.ID
	h.replySid = m.SessionID
	wrapped := map[string]any{"id": *m.ID, "result": res}
	if m.SessionID != "" {
		wrapped["sessionId"] = m.SessionID
	}
	h.reply = encode(wrapped)
	ask := g.askDocConfirmLocked(dc)
	return true, ask, g.takePendingPageLocked()
}
