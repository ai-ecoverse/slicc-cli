package cdp

import (
	"encoding/json"
	"time"
)

var documentConfirmEnabled = true

type docConfirm struct {
	target   string
	session  string
	parent   string
	typ      string
	waiting  bool
	event    []byte
	reply    []byte
	replyID  int64
	replySid string
	hasEvent bool
	hasReply bool
	ready    chan struct{}
	start    time.Time
	asking   bool
	done     bool
	failed   bool
}

func (g *guard) noteDocAttachLocked(target string) {
	if !documentConfirmEnabled || target == "" {
		return
	}
	if g.docAttach == nil {
		g.docAttach = map[string]bool{}
	}
	g.docAttach[target] = true
}

func (g *guard) clearDocAttachLocked(target string) {
	delete(g.docAttach, target)
}

func (g *guard) needsDocConfirmLocked(target, typ string) bool {
	if !documentConfirmEnabled || target == "" || typ == "browser" {
		return false
	}
	return g.docAttach[target]
}

func (g *guard) docConfirmForTargetLocked(target string) *docConfirm {
	for _, dc := range g.docConfirms {
		if dc != nil && dc.target == target && !dc.done {
			return dc
		}
	}
	return nil
}

func (g *guard) docConfirmWaitLocked(session string) <-chan struct{} {
	if session == "" || g.docConfirms == nil {
		return nil
	}
	dc := g.docConfirms[session]
	if dc == nil || dc.done {
		return nil
	}
	return dc.ready
}

func (g *guard) beginDocConfirmLocked(target, session, parent, typ string, waiting bool) *docConfirm {
	if g.docConfirms == nil {
		g.docConfirms = map[string]*docConfirm{}
	}
	if dc := g.docConfirms[session]; dc != nil {
		return dc
	}
	dc := &docConfirm{
		target:  target,
		session: session,
		parent:  parent,
		typ:     typ,
		waiting: waiting,
		ready:   make(chan struct{}),
		start:   time.Now(),
	}
	g.docConfirms[session] = dc
	return dc
}

func (g *guard) askDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc == nil || dc.asking || dc.done || g.relaySession == "" {
		return nil
	}
	dc.asking = true
	dc.start = time.Now()
	target := dc.target
	session := dc.session
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
		return g.answeredDocConfirmLocked(session, m)
	})}
}

func (g *guard) answeredDocConfirmLocked(session string, m message) [][]byte {
	dc := g.docConfirms[session]
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
		result.TargetInfo.Type = dc.typ
	}
	send := g.observeLocked(result.TargetInfo)
	v := g.infoVerdictLocked(result.TargetInfo)
	if g.targetProtectedLocked(dc.target) || v.loaded() {
		return append(send, g.failDocConfirmLocked(dc)...)
	}
	return append(send, g.passDocConfirmLocked(dc)...)
}

func (g *guard) failDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc.done {
		return nil
	}
	dc.done = true
	dc.failed = true
	g.clearDocAttachLocked(dc.target)
	if g.docFailed == nil {
		g.docFailed = map[string]bool{}
	}
	g.docFailed[dc.session] = true
	close(dc.ready)
	delete(g.docConfirms, dc.session)
	g.noteHiddenLocked(dc.session, dc.target)
	var send [][]byte
	if dc.waiting {
		send = append(send, g.resumeLocked(dc.session, dc.target))
	}
	parent := dc.parent
	if parent == "" {
		parent = g.attachParent
	}
	msg, _ := g.newCallLocked(parent, "Target.detachFromTarget", map[string]any{"sessionId": dc.session}, false)
	send = append(send, msg)
	if dc.hasReply {
		g.pendingPage = append(g.pendingPage, refusal(dc.replyID, dc.replySid, refusedTarget))
	}
	g.logf("cdp: refused an attach after confirming the target's current document")
	return send
}

func (g *guard) passDocConfirmLocked(dc *docConfirm) [][]byte {
	if dc.done {
		return nil
	}
	dc.done = true
	dc.failed = false
	g.clearDocAttachLocked(dc.target)
	close(dc.ready)
	delete(g.docConfirms, dc.session)
	var send [][]byte
	if t := g.targets[dc.target]; t != nil {
		if g.sessions[dc.session] == nil {
			g.sessions[dc.session] = &session{id: dc.session, target: dc.target, parent: dc.parent, typ: dc.typ, waiting: dc.waiting}
			if dc.typ == "browser" {
				g.browserSessions[dc.session] = true
			}
		}
		if workerTypes[t.typ] || t.typ == "iframe" {
			t.shown = true
		}
		g.markTouchedLocked(dc.target)
		send = append(send, g.touchLocked(dc.target, t.typ)...)
	} else {
		g.sessions[dc.session] = &session{id: dc.session, target: dc.target, parent: dc.parent, typ: dc.typ, waiting: dc.waiting}
		send = append(send, g.touchLocked(dc.target, dc.typ)...)
	}
	if dc.hasEvent && len(dc.event) > 0 {
		g.pendingPage = append(g.pendingPage, dc.event)
	}
	if dc.hasReply && len(dc.reply) > 0 {
		g.pendingPage = append(g.pendingPage, dc.reply)
	}
	return send
}

func (g *guard) docConfirmAttachEventLocked(m *message, data []byte, session string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	if g.docFailed[session] {
		return browserVerdict{send: send}, true
	}
	if !g.needsDocConfirmLocked(info.TargetID, info.Type) {
		return browserVerdict{}, false
	}
	if chromeErrorDocument(info.URL) {
		return browserVerdict{}, false
	}
	dc := g.beginDocConfirmLocked(info.TargetID, session, m.SessionID, info.Type, waiting)
	dc.hasEvent = true
	if params := decodeParams(m.Params); params != nil {
		if obj, _ := params["targetInfo"].(map[string]any); g.redactInfoLocked(obj, info) {
			m.Params = encode(params)
			dc.event = encode(m)
		} else {
			dc.event = append([]byte(nil), data...)
		}
	} else {
		dc.event = append([]byte(nil), data...)
	}
	ask := g.askDocConfirmLocked(dc)
	if dc.done && dc.failed {
		return browserVerdict{send: append(send, ask...)}, true
	}
	if dc.done && !dc.failed {
		page := g.takePendingPageLocked()
		return browserVerdict{forward: nil, send: append(send, ask...), page: page}, true
	}
	return browserVerdict{send: append(send, ask...)}, true
}

func (g *guard) holdDocConfirmReplyLocked(attached string, m *message, res map[string]any) (held bool, send [][]byte, page [][]byte) {
	sid, _ := res["sessionId"].(string)
	if sid == "" || sid == g.relaySession {
		return false, nil, nil
	}
	if g.docFailed[sid] {
		return true, nil, [][]byte{refusal(*m.ID, m.SessionID, refusedTarget)}
	}
	typ := ""
	if t := g.targets[attached]; t != nil {
		typ = t.typ
	}
	if !g.needsDocConfirmLocked(attached, typ) && g.docConfirmForTargetLocked(attached) == nil && g.docConfirms[sid] == nil {
		return false, nil, nil
	}
	if t := g.targets[attached]; t != nil && chromeErrorDocument(t.url) {
		return false, nil, nil
	}
	if typ == "browser" {
		return false, nil, nil
	}
	if typ == "" {
		typ = "page"
	}
	dc := g.docConfirms[sid]
	if dc == nil {
		dc = g.beginDocConfirmLocked(attached, sid, m.SessionID, typ, false)
	}
	if dc.done {
		if dc.failed {
			return true, nil, [][]byte{refusal(*m.ID, m.SessionID, refusedTarget)}
		}
		return false, nil, nil
	}
	dc.hasReply = true
	dc.replyID = *m.ID
	dc.replySid = m.SessionID
	wrapped := map[string]any{"id": *m.ID, "result": res}
	if m.SessionID != "" {
		wrapped["sessionId"] = m.SessionID
	}
	dc.reply = encode(wrapped)
	ask := g.askDocConfirmLocked(dc)
	return true, ask, nil
}
