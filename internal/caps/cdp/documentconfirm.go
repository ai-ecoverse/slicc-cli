package cdp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
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
	target       string
	holds        map[string]*docHold
	ready        chan struct{}
	start        time.Time
	deadline     time.Time
	timer        *time.Timer
	asking       bool
	done         bool
	failed       bool
	latched      bool
	proofSession string
	settle       bool
	failedLoad   bool
	lastURL      string
	lastOrigin   string
	transition   bool
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

func (g *guard) docConfirmOwnedLocked(target string) bool {
	if target == "" || docConfirmDropProvenanceEnabled() {
		return false
	}
	if g.created[target] {
		return true
	}
	if g.opened[target] {
		return true
	}
	return g.errorProofOfLocked(target) == errorProofOrdinary
}

func (g *guard) docConfirmURLNeedsProofLocked(url string) bool {
	if docConfirmExemptBlanknessEnabled() && (url == "" || url == "about:blank" || strings.HasPrefix(strings.ToLower(url), "about:blank")) {
		return false
	}
	if url == "" {
		return true
	}
	lower := strings.ToLower(url)
	if lower == "about:blank" || strings.HasPrefix(lower, "about:blank") {
		return true
	}
	if chromeErrorDocument(url) {
		return true
	}
	if protect.Unparseable(url) {
		return true
	}
	if strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:") {
		return true
	}
	return false
}

func (g *guard) docConfirmForeignNeedsProofLocked(url, target string) bool {
	if g.docConfirmOwnedLocked(target) {
		return false
	}
	if docConfirmDropDKCoverageEnabled() {
		return false
	}
	if g.docConfirmURLNeedsProofLocked(url) {
		return true
	}
	lower := strings.ToLower(url)
	if url == "" || lower == "about:blank" || strings.HasPrefix(lower, "about:blank") {
		return false
	}
	return docConfirmForeignOrdinaryProofEnabled()
}

func (g *guard) needsDocConfirmLocked(target, session, typ, url string) bool {
	if target == "" || typ == "browser" {
		return false
	}
	if workerTypes[typ] || typ == "iframe" {
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
	if typ != "page" {
		return false
	}
	if url == "" {
		if t := g.targets[target]; t != nil {
			url = t.url
		}
	}
	return g.docConfirmForeignNeedsProofLocked(url, target)
}

func (g *guard) docConfirmSkipChromeErrorLocked(target string) bool {
	return g.errorProofOfLocked(target) == errorProofOrdinary
}

func (g *guard) docConfirmLiveProtectedDocumentLocked(url string) bool {
	if url == "" || chromeErrorDocument(url) || g.o == nil || !g.o.Loaded(url) {
		return false
	}
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:")
}

func (g *guard) docConfirmOrdinaryDocumentLocked(url string) bool {
	if url == "" || chromeErrorDocument(url) || protect.Unparseable(url) {
		return false
	}
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:") {
		return false
	}
	if lower == "about:blank" || strings.HasPrefix(lower, "about:blank") {
		return false
	}
	if protect.Inheriting(url) {
		return true
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return g.o == nil || !g.o.Loaded(url)
	}
	return false
}

func (g *guard) docConfirmBlankURLLocked(url string) bool {
	if url == "" {
		return false
	}
	lower := strings.ToLower(url)
	return lower == "about:blank" || strings.HasPrefix(lower, "about:blank")
}

func (g *guard) docConfirmOpaqueOriginLocked(origin string) bool {
	return origin == "" || origin == "://" || origin == "null"
}

func (g *guard) docConfirmProtectedOriginLocked(origin string) bool {
	if g.docConfirmOpaqueOriginLocked(origin) {
		return false
	}
	return g.o != nil && g.o.Loaded(origin)
}

func (g *guard) docConfirmBlankOrdinaryOriginLocked(origin string) bool {
	if !docConfirmAdmitBlankOrdinaryEnabled() {
		return false
	}
	return !g.docConfirmProtectedOriginLocked(origin)
}

func (g *guard) docConfirmTransitionFrameLocked(frame confirmFrame) bool {
	return frame.URL == "" && frame.UnreachableURL == ""
}

func (g *guard) logDocConfirmAvailabilityLocked() {
	g.logf("cdp: DIAG document-confirm availability refused-ordinary=%d refused-blank=%d", g.docRefusedOrdinary, g.docRefusedBlank)
}

func (g *guard) noteDocConfirmAvailabilityRefuseLocked(url, origin string, latch bool) {
	if latch {
		g.logDocConfirmAvailabilityLocked()
		return
	}
	if g.docConfirmOrdinaryDocumentLocked(url) {
		g.docRefusedOrdinary++
	}
	if g.docConfirmBlankURLLocked(url) && !g.docConfirmProtectedOriginLocked(origin) {
		g.docRefusedBlank++
	}
	g.logDocConfirmAvailabilityLocked()
}

func (g *guard) docConfirmRefuseLocked(id string, info targetInfo) bool {
	if g.workerConcealedLocked(id) || g.iframeConcealedLocked(id) {
		return true
	}
	if g.targetProtectedLocked(id) {
		return true
	}
	if g.docConfirmLiveProtectedDocumentLocked(info.URL) {
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
	send := g.refuseDocConfirmLocked(dc, false)
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
	if docConfirmDropProofEnabled() {
		return g.passDocConfirmLocked(dc)
	}
	if g.relaySession == "" {
		return g.refuseDocConfirmLocked(dc, false)
	}
	dc.asking = true
	dc.start = time.Now()
	target := dc.target
	if g.docProofPending == nil {
		g.docProofPending = map[string]bool{}
	}
	g.docProofPending[target] = true
	return [][]byte{g.callbackLocked(g.relaySession, "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, func(m message) [][]byte {
		return g.docConfirmProofAttachedLocked(target, m)
	})}
}

func (g *guard) docConfirmProofAttachedLocked(target string, m message) [][]byte {
	dc := g.docInflight[target]
	if dc == nil || dc.done {
		delete(g.docProofPending, target)
		return nil
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || res.SessionID == "" {
		delete(g.docProofPending, target)
		dc.asking = false
		return g.refuseDocConfirmLocked(dc, false)
	}
	dc.proofSession = res.SessionID
	if g.docProofSessions == nil {
		g.docProofSessions = map[string]string{}
	}
	g.docProofSessions[res.SessionID] = target
	delete(g.docProofPending, target)
	return [][]byte{g.callbackLocked(res.SessionID, "Page.getFrameTree", nil, func(tree message) [][]byte {
		return g.docConfirmFrameTreeLocked(target, tree)
	})}
}

func (g *guard) docConfirmFrameTreeLocked(target string, m message) [][]byte {
	dc := g.docInflight[target]
	if dc == nil || dc.done {
		return g.docConfirmDetachProofLocked(dc)
	}
	g.logf("cdp: DIAG document-confirm proof round took %s", time.Since(dc.start).Round(time.Millisecond))
	dc.asking = false
	var result struct {
		FrameTree struct {
			Frame confirmFrame `json:"frame"`
		} `json:"frameTree"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil {
		send := g.refuseDocConfirmLocked(dc, false)
		return append(g.docConfirmDetachProofLocked(dc), send...)
	}
	frame := result.FrameTree.Frame
	if g.docConfirmTransitionFrameLocked(frame) {
		dc.transition = true
		dc.lastURL = ""
		dc.lastOrigin = frame.SecurityOrigin
		g.logf("cdp: DIAG document-confirm transition park target=%s", target)
		g.logDocConfirmAvailabilityLocked()
		return g.docConfirmDetachProofLocked(dc)
	}
	url := frame.URL
	if url == "" {
		url = frame.UnreachableURL
	}
	origin := frame.SecurityOrigin
	dc.lastURL = url
	dc.lastOrigin = origin
	dc.transition = false
	info := targetInfo{TargetID: target, URL: url, Type: "page"}
	if t := g.targets[target]; t != nil {
		info.Type = t.typ
	}
	for _, h := range dc.holds {
		if h.typ != "" {
			info.Type = h.typ
			break
		}
	}
	send := g.observeLocked(info)
	if t := g.targets[target]; t != nil {
		t.protected = g.decide(t, t.protected)
	}
	send = append(send, g.docConfirmDetachProofLocked(dc)...)
	protectedEvidence := g.docConfirmRefuseLocked(dc.target, info) || g.docConfirmLiveProtectedDocumentLocked(url) || (g.o != nil && g.o.Loaded(url))
	if g.docConfirmBlankURLLocked(url) && g.docConfirmProtectedOriginLocked(origin) {
		protectedEvidence = true
	}
	if protectedEvidence {
		return append(send, g.refuseDocConfirmLocked(dc, true)...)
	}
	if g.docConfirmBlankURLLocked(url) {
		if g.docConfirmBlankOrdinaryOriginLocked(origin) {
			out := g.passDocConfirmLocked(dc)
			g.logDocConfirmAvailabilityLocked()
			return append(send, out...)
		}
		return append(send, g.refuseDocConfirmLocked(dc, docConfirmLatchOnFailureEnabled())...)
	}
	if !g.docConfirmOrdinaryDocumentLocked(url) {
		return append(send, g.refuseDocConfirmLocked(dc, docConfirmLatchOnFailureEnabled())...)
	}
	out := g.passDocConfirmLocked(dc)
	g.logDocConfirmAvailabilityLocked()
	return append(send, out...)
}

func (g *guard) docConfirmMaybeReproofLocked(target string) [][]byte {
	dc := g.docInflight[target]
	if dc == nil || dc.done || dc.asking || !dc.transition {
		return nil
	}
	return g.askDocConfirmLocked(dc)
}

func (g *guard) docConfirmDetachProofLocked(dc *docConfirm) [][]byte {
	if dc == nil || dc.proofSession == "" {
		return nil
	}
	sid := dc.proofSession
	dc.proofSession = ""
	delete(g.docProofSessions, sid)
	msg, _ := g.newCallLocked(g.relaySession, "Target.detachFromTarget", map[string]any{"sessionId": sid}, false)
	return [][]byte{msg}
}

func (g *guard) docConfirmProofSessionLocked(session string) bool {
	return session != "" && g.docProofSessions[session] != ""
}

func (g *guard) docConfirmProofPendingLocked(target string) bool {
	return target != "" && g.docProofPending[target]
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
	delete(g.docProofPending, dc.target)
	_ = g.docConfirmDetachProofLocked(dc)
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

func (g *guard) refuseDocConfirmLocked(dc *docConfirm, latch bool) [][]byte {
	if dc == nil || dc.done {
		return nil
	}
	dc.done = true
	dc.failed = true
	dc.latched = latch
	g.stopDocTimerLocked(dc)
	close(dc.ready)
	delete(g.docInflight, dc.target)
	delete(g.docProofPending, dc.target)
	var send [][]byte
	send = append(send, g.docConfirmDetachProofLocked(dc)...)
	var failed []string
	if latch {
		if g.docFailed == nil {
			g.docFailed = map[string]bool{}
		}
	}
	for sid, h := range dc.holds {
		if latch {
			g.docFailed[sid] = true
			failed = append(failed, sid)
			g.noteHiddenLocked(sid, dc.target)
		}
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
	g.noteDocConfirmAvailabilityRefuseLocked(dc.lastURL, dc.lastOrigin, latch)
	if latch {
		g.scheduleDocFailedClearLocked(failed)
		g.logf("cdp: refused an attach after confirming a protected document")
	} else {
		g.logf("cdp: refused an attach after the document proof failed")
	}
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
	delete(g.docProofPending, dc.target)
	if g.docPassed == nil {
		g.docPassed = map[string]bool{}
	}
	var send [][]byte
	send = append(send, g.docConfirmDetachProofLocked(dc)...)
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
	delete(g.docProofSessions, session)
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
		g.refuseDocConfirmLocked(dc, false)
	}
	delete(g.created, target)
	delete(g.docProofPending, target)
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
	if g.docConfirmProofSessionLocked(session) || g.docConfirmProofPendingLocked(info.TargetID) {
		return browserVerdict{send: send}, true
	}
	if g.docFailed[session] {
		return browserVerdict{send: send}, true
	}
	if g.docPassed[session] {
		return browserVerdict{}, false
	}
	if !g.needsDocConfirmLocked(info.TargetID, session, info.Type, info.URL) {
		return browserVerdict{}, false
	}
	if g.docConfirmSkipChromeErrorLocked(info.TargetID) {
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
	if g.docConfirmProofSessionLocked(sid) {
		return false, nil, nil
	}
	if g.docFailed[sid] {
		return true, nil, [][]byte{refusal(*m.ID, m.SessionID, refusedTarget)}
	}
	if g.docPassed[sid] {
		return false, nil, nil
	}
	typ := ""
	url := ""
	if t := g.targets[attached]; t != nil {
		typ = t.typ
		url = t.url
	}
	if typ == "browser" {
		return false, nil, nil
	}
	if typ == "" {
		typ = "page"
	}
	if g.docConfirmSkipChromeErrorLocked(attached) {
		return false, nil, nil
	}
	if !g.needsDocConfirmLocked(attached, sid, typ, url) {
		if dc := g.docInflight[attached]; dc == nil || dc.done {
			return false, nil, nil
		}
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
	if !g.needsDocConfirmLocked(attached, sid, typ, url) {
		return false, nil, nil
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

func (g *guard) settleDocConfirmHeldLocked(h *heldSession) browserVerdict {
	if h == nil {
		return browserVerdict{}
	}
	failedLoad := g.errorProofOfLocked(h.target) != errorProofNone
	if failedLoad && docConfirmDropSettleFailedLoadEnabled() {
		return g.admitHeldLocked(h)
	}
	if !failedLoad && docConfirmDropSettleEmptyHoldEnabled() {
		return g.admitHeldLocked(h)
	}
	if g.docConfirmOwnedLocked(h.target) {
		return g.admitHeldLocked(h)
	}
	url := ""
	if t := g.targets[h.target]; t != nil {
		url = t.url
	}
	if g.targetProtectedLocked(h.target) || g.docConfirmLiveProtectedDocumentLocked(url) {
		return g.hideHeldPageAndTabsLocked(h, true, "protected hide")
	}
	dc := g.beginDocConfirmLocked(h.target, h.id, h.parent, h.typ, h.waiting)
	dc.settle = true
	dc.failedLoad = failedLoad
	hold := dc.holds[h.id]
	hold.hasEvent = true
	if h.params == nil {
		h.params = map[string]any{}
	}
	if t := g.targets[h.target]; t != nil {
		if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
			obj["url"] = t.url
			obj["type"] = t.typ
			obj["targetId"] = t.id
		} else {
			h.params["targetInfo"] = map[string]any{"targetId": t.id, "type": t.typ, "url": t.url}
		}
	}
	h.params["waitingForDebugger"] = h.waiting
	ev := map[string]any{"method": "Target.attachedToTarget", "params": h.params}
	if h.parent != "" {
		ev["sessionId"] = h.parent
	}
	hold.event = encode(ev)
	ask := g.askDocConfirmLocked(dc)
	return browserVerdict{send: ask, page: g.takePendingPageLocked()}
}

func (g *guard) admitHeldLocked(h *heldSession) browserVerdict {
	var v browserVerdict
	if h == nil {
		return v
	}
	waiting := false
	switch {
	case workerTypes[h.typ]:
		waiting = h.waiting
		g.logf("cdp: released an auto-attached worker after its URL or creator frame was known target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, waiting)
	case h.typ == "iframe":
		waiting = h.waiting && !h.resumed
		g.logf("cdp: released an auto-attached iframe after its URL and parent target were known target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, waiting)
	default:
		g.logf("cdp: released an auto-attached session after its target got a URL target=%s session=%s parent=%s", h.target, h.id, h.parent)
	}
	if t := g.targets[h.target]; t != nil && (workerTypes[t.typ] || t.typ == "iframe") {
		t.shown = true
	}
	g.sessions[h.id] = &session{id: h.id, target: h.target, parent: h.parent, typ: h.typ, waiting: waiting, at: time.Now()}
	v.send = append(v.send, g.touchLocked(h.target, h.typ)...)
	if h.params == nil {
		h.params = map[string]any{}
	}
	if t := g.targets[h.target]; t != nil {
		if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
			obj["url"] = t.url
			obj["type"] = t.typ
			obj["targetId"] = t.id
			if t.parentFrame != "" {
				obj["parentFrameId"] = t.parentFrame
			}
		} else {
			h.params["targetInfo"] = map[string]any{"targetId": t.id, "type": t.typ, "url": t.url, "parentFrameId": t.parentFrame}
		}
	}
	if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
		ti, _ := toInfo(obj)
		g.redactInfoLocked(obj, ti)
	}
	h.params["waitingForDebugger"] = waiting
	ev := map[string]any{"method": "Target.attachedToTarget", "params": h.params}
	if h.parent != "" {
		ev["sessionId"] = h.parent
	}
	v.page = append(v.page, encode(ev))
	return v
}
