package cdp

import (
	"encoding/json"
	"time"
)

const (
	HoldTimeout    = 10 * time.Second
	HoldTimeoutEnv = "SLICC_CDP_HOLD_TIMEOUT"
)

func holdTimeout() time.Duration {
	return envDuration(HoldTimeoutEnv, HoldTimeout)
}

type heldSession struct {
	id         string
	parent     string
	adoptedTab string
	target     string
	typ        string
	params     map[string]any
	waiting    bool
	resumed    bool
	at         time.Time
}

func (g *guard) holdLocked(parent string, params map[string]any, info targetInfo, waiting bool) [][]byte {
	sid, _ := params["sessionId"].(string)
	h := &heldSession{id: sid, parent: parent, target: info.TargetID, typ: info.Type, params: params, waiting: waiting, at: time.Now()}
	if !holdResumeUnheldEnabled() {
		g.held[sid] = h
	}
	if workerTypes[info.Type] {
		g.logf("cdp: holding an auto-attached worker until its URL or creator frame is known target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
		return nil
	}
	if info.Type == "iframe" && g.iframeUnresolvedLocked(g.targets[info.TargetID]) {
		g.logf("cdp: holding an auto-attached iframe until its parent target is known target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
	} else {
		g.logf("cdp: holding an auto-attached session until its target has a URL target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
	}
	shouldResume := waiting
	if info.Type == "page" && holdResumeRegardlessEnabled() {
		shouldResume = true
	}
	if !shouldResume {
		return nil
	}
	if h.resumed {
		return nil
	}
	h.resumed = true
	g.noteHoldResumedLocked(sid)
	out := [][]byte{g.resumeLocked(sid, info.TargetID)}
	if holdResumeTwiceEnabled() {
		out = append(out, g.resumeLocked(sid, info.TargetID))
	}
	return out
}

func (g *guard) noteHoldResumedLocked(sid string) {
	if sid == "" {
		return
	}
	if g.holdResumed == nil {
		g.holdResumed = map[string]bool{}
	}
	if g.holdResumed[sid] {
		return
	}
	g.holdResumed[sid] = true
	g.holdResumedOrder = append(g.holdResumedOrder, sid)
	if !holdResumeCapEnabled() {
		return
	}
	limit := holdResumeCapLimit()
	if limit <= 0 {
		return
	}
	for len(g.holdResumedOrder) > limit {
		old := g.holdResumedOrder[0]
		g.holdResumedOrder = g.holdResumedOrder[1:]
		delete(g.holdResumed, old)
	}
}

func (g *guard) forgetHoldResumedLocked(id string) {
	if id == "" || !g.holdResumed[id] {
		return
	}
	delete(g.holdResumed, id)
	for i, sid := range g.holdResumedOrder {
		if sid != id {
			continue
		}
		g.holdResumedOrder = append(g.holdResumedOrder[:i], g.holdResumedOrder[i+1:]...)
		return
	}
}

func (g *guard) answerHoldResumeLocked(c *command) []byte {
	if c == nil || c.method != "Runtime.runIfWaitingForDebugger" {
		return nil
	}
	if h := g.held[c.session]; h != nil {
		if h.resumed {
			return acknowledgement(c.id, c.session)
		}
		return nil
	}
	if g.holdResumed[c.session] {
		return acknowledgement(c.id, c.session)
	}
	return nil
}

func heldOpenerLocked(h *heldSession) string {
	if h == nil || h.params == nil {
		return ""
	}
	obj, _ := h.params["targetInfo"].(map[string]any)
	if obj == nil {
		return ""
	}
	s, _ := obj["openerId"].(string)
	return s
}

func (g *guard) tabHasNestedPageChildLocked(sid string) bool {
	for _, h := range g.held {
		if h != nil && h.typ == "page" && h.parent == sid {
			return true
		}
	}
	for _, s := range g.sessions {
		if s != nil && s.parent == sid && s.typ == "page" {
			return true
		}
	}
	return false
}

func (g *guard) unmatchedEmptyTabCandidatesInWindowLocked(holdStart, end time.Time) []string {
	if end.IsZero() {
		end = time.Now()
	}
	if holdStart.IsZero() {
		holdStart = end
	}
	windowStart := holdStart.Add(-holdTimeout())
	var out []string
	for sid, s := range g.sessions {
		if s == nil || s.typ != "tab" || s.refused || !s.emptyAtAnnounce {
			continue
		}
		if g.tabHasNestedPageChildLocked(sid) {
			continue
		}
		t := g.targets[s.target]
		if t == nil || t.url != "" {
			continue
		}
		if s.at.Before(windowStart) || s.at.After(end) {
			continue
		}
		out = append(out, sid)
	}
	return out
}

func (g *guard) browserRootSessionLocked(parent string) bool {
	return parent == "" || g.browserSessions[parent]
}

func (g *guard) adoptHeldFlatPageLocked(tabSession, tabOpener string) {
	if tabSession == "" || tabOpener == "" {
		return
	}
	var cand *heldSession
	for _, h := range g.held {
		if h == nil || h.typ != "page" || !g.browserRootSessionLocked(h.parent) || h.adoptedTab != "" {
			continue
		}
		if heldOpenerLocked(h) != tabOpener {
			continue
		}
		if cand != nil {
			return
		}
		cand = h
	}
	if cand == nil {
		return
	}
	cand.adoptedTab = tabSession
}

func (g *guard) detachAnnouncedTabSessionLocked(sid string, protected bool) browserVerdict {
	s := g.sessions[sid]
	if s == nil || s.typ != "tab" {
		return browserVerdict{}
	}
	target, parent, waiting := s.target, s.parent, s.waiting
	s.refused = true
	if protected {
		g.addRefusedLocked(sid)
		g.logf("cdp: detached an auto-attached session on a protected origin target=%s session=%s parent=%s waiting=%t", target, sid, parent, waiting)
	} else {
		g.logf("cdp: detached an auto-attached session that never got a URL target=%s session=%s parent=%s waiting=%t", target, sid, parent, waiting)
	}
	var send [][]byte
	if waiting {
		send = append(send, g.resumeLocked(sid, target))
	}
	msg, _ := g.newCallLocked(parent, "Target.detachFromTarget", map[string]any{"sessionId": sid}, false)
	send = append(send, msg)
	page := [][]byte{g.detachEventLocked(sid, target, parent)}
	orphanPage, orphanSend := g.cascadeOrphansLocked(sid)
	page = append(page, orphanPage...)
	send = append(send, orphanSend...)
	page = append(page, g.takePendingMissingLocked(sid)...)
	g.navDetachedLocked(sid)
	g.forgetSessionLocked(sid)
	return browserVerdict{send: send, page: page}
}

func (g *guard) detachUnmatchedEmptyTabsInWindowLocked(holdStart, end time.Time, protected bool, why, heldPage string) browserVerdict {
	if end.IsZero() {
		end = time.Now()
	}
	if holdStart.IsZero() {
		holdStart = end
	}
	windowStart := holdStart.Add(-holdTimeout())
	var v browserVerdict
	for _, sid := range g.unmatchedEmptyTabCandidatesInWindowLocked(holdStart, end) {
		target := ""
		if s := g.sessions[sid]; s != nil {
			target = s.target
		}
		g.logf("cdp: detached an unmatched empty tab by hold-window match target=%s session=%s held=%s window=%s..%s why=%s", target, sid, heldPage, windowStart.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), why)
		part := g.detachAnnouncedTabSessionLocked(sid, protected)
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
	}
	return v
}

func (g *guard) detachUnmatchedEmptyTabsAroundLocked(at time.Time, protected bool, why, heldPage string) browserVerdict {
	if at.IsZero() {
		at = time.Now()
	}
	return g.detachUnmatchedEmptyTabsInWindowLocked(at, at, protected, why, heldPage)
}

func (g *guard) detachUnmatchedEmptyTabsForHeldLocked(h *heldSession, protected bool, why string) browserVerdict {
	holdStart := time.Time{}
	heldPage := ""
	if h != nil {
		holdStart = h.at
		heldPage = h.target
	}
	return g.detachUnmatchedEmptyTabsInWindowLocked(holdStart, time.Now(), protected, why, heldPage)
}

func (g *guard) detachAnnouncedTabsForPageLocked(h *heldSession, protected bool, why string) browserVerdict {
	if h == nil || h.typ != "page" {
		return browserVerdict{}
	}
	if !g.browserRootSessionLocked(h.parent) {
		return g.detachAnnouncedTabSessionLocked(h.parent, protected)
	}
	if h.adoptedTab != "" {
		return g.detachAnnouncedTabSessionLocked(h.adoptedTab, protected)
	}
	return g.detachUnmatchedEmptyTabsForHeldLocked(h, protected, why)
}

func (g *guard) hideHeldPageAndTabsLocked(h *heldSession, protected bool, why string) browserVerdict {
	var v browserVerdict
	if h == nil {
		return v
	}
	if protected && holdResumeChangesProtectedFateEnabled() {
		var was bool
		if t := g.targets[h.target]; t != nil {
			was = t.protected
			t.protected = false
		}
		part := g.deliverHeldLocked(h)
		if t := g.targets[h.target]; t != nil {
			t.protected = was
		}
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
		return v
	}
	if protected {
		v.send = append(v.send, g.hideLocked(h)...)
	} else {
		v.send = append(v.send, g.expireDetachLocked(h)...)
	}
	if holdResumeRefusedEnabled() && protected {
		v.send = append(v.send, g.resumeLocked(h.id, h.target))
	}
	part := g.detachAnnouncedTabsForPageLocked(h, protected, why)
	v.send = append(v.send, part.send...)
	v.page = append(v.page, part.page...)
	return v
}

func (g *guard) releaseLocked(targetID string, info any) browserVerdict {
	var v browserVerdict
	var matched []*heldSession
	for _, h := range g.held {
		if h.target != targetID {
			continue
		}
		if workerTypes[h.typ] && g.workerUnresolvedLocked(g.targets[targetID]) {
			continue
		}
		if h.typ == "iframe" && g.iframePendingLocked(g.targets[targetID]) {
			continue
		}
		if g.keepHeldForFailedLoadLocked(h, info) {
			v.send = append(v.send, g.armFailedLoadProofLocked(g.targets[targetID])...)
			continue
		}
		matched = append(matched, h)
	}
	hide := g.targetProtectedLocked(targetID)
	for _, h := range matched {
		if g.held[h.id] == nil {
			continue
		}
		if h.params != nil && info != nil {
			h.params["targetInfo"] = info
		}
		delete(g.held, h.id)
		if hide {
			part := g.hideHeldPageAndTabsLocked(h, true, "protected hide")
			v.send = append(v.send, part.send...)
			v.page = append(v.page, part.page...)
			continue
		}
		part := g.deliverHeldLocked(h)
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
	}
	return v
}

func (g *guard) deliverHeldLocked(h *heldSession) browserVerdict {
	delete(g.held, h.id)
	if g.targetProtectedLocked(h.target) {
		return g.hideHeldPageAndTabsLocked(h, true, "protected hide")
	}
	if h.typ == "page" {
		return g.settleDocConfirmHeldLocked(h)
	}
	return g.admitHeldLocked(h)
}

func (g *guard) settleHeldWorkersLocked() browserVerdict {
	var v browserVerdict
	ids := make([]string, 0, len(g.held))
	for sid, h := range g.held {
		if workerTypes[h.typ] || h.typ == "iframe" {
			ids = append(ids, sid)
		}
	}
	for _, sid := range ids {
		h := g.held[sid]
		if h == nil {
			continue
		}
		t := g.targets[h.target]
		if t == nil {
			continue
		}
		if workerTypes[h.typ] && g.workerUnresolvedLocked(t) {
			continue
		}
		if h.typ == "iframe" && g.iframeUnresolvedLocked(t) {
			continue
		}
		prev := t.protected
		t.protected = g.decide(t, prev)
		if t.protected && !prev {
			v.send = append(v.send, g.protectLocked(t)...)
		}
		if workerTypes[h.typ] && g.workerUnresolvedLocked(t) {
			continue
		}
		if h.typ == "iframe" && g.iframePendingLocked(t) {
			continue
		}
		part := g.deliverHeldLocked(h)
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
	}
	return v
}

func (g *guard) resumeLocked(sessionID, targetID string) []byte {
	return g.callbackLocked(sessionID, "Runtime.runIfWaitingForDebugger", nil, func(m message) [][]byte {
		if m.Error != nil {
			g.logf("cdp: could not resume an auto-attached session target=%s session=%s", targetID, sessionID)
		} else {
			g.logf("cdp: resumed an auto-attached session target=%s session=%s", targetID, sessionID)
		}
		return nil
	})
}

func (g *guard) noteHiddenLocked(id, target string) {
	if id == "" {
		return
	}
	g.hidden[id] = true
	if target != "" {
		g.hiddenOwner[id] = target
	}
}

func (g *guard) hideLocked(h *heldSession) [][]byte {
	g.refuseHeldWaitLocked(h.target)
	g.noteHiddenLocked(h.id, h.target)
	g.addRefusedLocked(h.id)
	g.logf("cdp: detached an auto-attached session on a protected origin target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	var send [][]byte
	if h.waiting && !h.resumed {
		h.resumed = true
		g.noteHoldResumedLocked(h.id)
		send = append(send, g.resumeLocked(h.id, h.target))
	}
	msg, _ := g.newCallLocked(h.parent, "Target.detachFromTarget", map[string]any{"sessionId": h.id}, false)
	return append(send, msg)
}

func autoAttachStopped(c *command) bool {
	if c == nil || c.method != "Target.setAutoAttach" || c.session == "" || c.params == nil {
		return false
	}
	if _, ok := c.params["filter"]; ok {
		return false
	}
	on, ok1 := c.params["autoAttach"].(bool)
	wait, ok2 := c.params["waitForDebuggerOnStart"].(bool)
	flat, ok3 := c.params["flatten"].(bool)
	return ok1 && ok2 && ok3 && !on && !wait && flat
}

func (g *guard) releaseOnAutoAttachStopLocked(c *command) [][]byte {
	switch {
	case c.method == "Target.detachFromTarget":
		return g.releaseHeldWorkersForSessionLocked(c.str("sessionId"), "its page session detached")
	case autoAttachStopped(c):
		return g.releaseHeldWorkersForSessionLocked(c.session, "auto-attach was turned off")
	default:
		return nil
	}
}

func (g *guard) releaseHeldWorkersForSessionLocked(session, why string) [][]byte {
	if session == "" {
		return nil
	}
	var send [][]byte
	for sid, h := range g.held {
		if h == nil || h.parent != session || (!workerTypes[h.typ] && h.typ != "iframe" && h.typ != "page") {
			continue
		}
		delete(g.held, sid)
		kind := "worker"
		switch h.typ {
		case "iframe":
			kind = "iframe"
		case "page":
			kind = "page"
		}
		g.logf("cdp: releasing a held %s because %s target=%s session=%s parent=%s waiting=%t", kind, why, h.target, h.id, h.parent, h.waiting)
		send = append(send, g.hideLocked(h)...)
		if h.typ == "page" {
			g.clearNeverSeenHeldPageLocked(h.id)
		}
	}
	return send
}

func (g *guard) clearNeverSeenHeldPageLocked(id string) {
	delete(g.refused, id)
	delete(g.hidden, id)
	delete(g.hiddenOwner, id)
	g.goneLocked(id)
}

func (g *guard) expireDetachLocked(h *heldSession) [][]byte {
	g.refuseHeldWaitLocked(h.target)
	g.noteHiddenLocked(h.id, h.target)
	g.logf("cdp: detached an auto-attached session that never got a URL target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	var send [][]byte
	if h.waiting && !h.resumed {
		h.resumed = true
		g.noteHoldResumedLocked(h.id)
		send = append(send, g.resumeLocked(h.id, h.target))
	}
	msg, _ := g.newCallLocked(h.parent, "Target.detachFromTarget", map[string]any{"sessionId": h.id}, false)
	return append(send, msg)
}

func (g *guard) expireHold(sessionID string) (page, send [][]byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.held[sessionID]
	if !ok {
		return nil, nil
	}
	delete(g.held, sessionID)
	g.logf("cdp: an auto-attached target never got a URL target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	part := g.hideHeldPageAndTabsLocked(h, false, "unclassified expiry")
	g.resolveUnknownLocked()
	return part.page, part.send
}

func (g *guard) dropHeldLocked(targetID string) (page, send [][]byte) {
	for sid, h := range g.held {
		if h.target != targetID {
			continue
		}
		delete(g.held, sid)
		part := g.hideHeldPageAndTabsLocked(h, true, "destroy-while-held")
		send = append(send, part.send...)
		page = append(page, part.page...)
	}
	return page, send
}

func (g *guard) blockChildLocked(parent, frame string) [][]byte {
	if parent == "" || frame == "" || frame == parent {
		return nil
	}
	if len(g.blockedChildren) < maxMarked {
		g.blockedChildren[frame] = parent
	}
	var send [][]byte
	for sid, h := range g.held {
		if h.target != frame {
			continue
		}
		delete(g.held, sid)
		part := g.hideHeldPageAndTabsLocked(h, true, "protected hide")
		send = append(send, part.send...)
		g.pendingPage = append(g.pendingPage, part.page...)
	}
	g.resolveUnknownLocked()
	return send
}

func (g *guard) clearBlockedChildrenLocked(targetID string) {
	delete(g.blockedChildren, targetID)
	for frame, parent := range g.blockedChildren {
		if parent == targetID {
			delete(g.blockedChildren, frame)
		}
	}
}

func decodeParams(raw json.RawMessage) map[string]any {
	p, _ := decodeObject(raw)
	return p
}
