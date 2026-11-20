package cdp

import (
	"encoding/json"
	"slices"
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
	id      string
	parent  string
	target  string
	typ     string
	params  map[string]any
	waiting bool
	resumed bool
	at      time.Time
}

func (g *guard) holdLocked(parent string, params map[string]any, info targetInfo, waiting bool) [][]byte {
	sid, _ := params["sessionId"].(string)
	h := &heldSession{id: sid, parent: parent, target: info.TargetID, typ: info.Type, params: params, waiting: waiting, at: time.Now()}
	g.held[sid] = h
	if workerTypes[info.Type] {
		g.logf("cdp: holding an auto-attached worker until its URL or creator frame is known target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
		return nil
	}
	if info.Type == "iframe" && g.iframeUnresolvedLocked(g.targets[info.TargetID]) {
		g.logf("cdp: holding an auto-attached iframe until its parent target is known target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
	} else {
		g.logf("cdp: holding an auto-attached session until its target has a URL target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
	}
	if !waiting {
		return nil
	}
	h.resumed = true
	return [][]byte{g.resumeLocked(sid, info.TargetID)}
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

func (g *guard) emptyURLPopupGroupLocked(h *heldSession) []*heldSession {
	if h == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*heldSession
	add := func(x *heldSession) {
		if x == nil || seen[x.id] {
			return
		}
		seen[x.id] = true
		out = append(out, x)
	}
	add(h)
	if h.parent != "" {
		add(g.held[h.parent])
	}
	for _, o := range g.held {
		if o != nil && o.parent == h.id {
			add(o)
		}
	}
	if opener := heldOpenerLocked(h); opener != "" {
		for _, o := range g.held {
			if o == nil || (o.typ != "tab" && o.typ != "page") {
				continue
			}
			if heldOpenerLocked(o) == opener {
				add(o)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b *heldSession) int {
		rank := func(h *heldSession) int {
			switch h.typ {
			case "tab":
				return 0
			case "page":
				return 1
			default:
				return 2
			}
		}
		return rank(a) - rank(b)
	})
	return out
}

func (g *guard) releaseMatchHeldLocked(targetID string, info any, v *browserVerdict) []*heldSession {
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
	return matched
}

func (g *guard) releasePopupGroupLocked(targetID string, info any, group []*heldSession, hide bool, released map[string]bool, v *browserVerdict) {
	for _, gh := range group {
		if gh == nil || released[gh.id] || g.held[gh.id] == nil {
			continue
		}
		if gh.params != nil && info != nil && gh.target == targetID {
			gh.params["targetInfo"] = info
		}
		delete(g.held, gh.id)
		released[gh.id] = true
		if hide {
			v.send = append(v.send, g.hideLocked(gh)...)
			continue
		}
		part := g.deliverHeldLocked(gh)
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
	}
}

func (g *guard) releaseLocked(targetID string, info any) browserVerdict {
	var v browserVerdict
	matched := g.releaseMatchHeldLocked(targetID, info, &v)
	released := map[string]bool{}
	hide := g.targetProtectedLocked(targetID)
	for _, h := range matched {
		if released[h.id] || g.held[h.id] == nil {
			continue
		}
		if h.params != nil && info != nil && h.target == targetID {
			h.params["targetInfo"] = info
		}
		g.releasePopupGroupLocked(targetID, info, g.emptyURLPopupGroupLocked(h), hide, released, &v)
	}
	return v
}

func (g *guard) deliverHeldLocked(h *heldSession) browserVerdict {
	var v browserVerdict
	delete(g.held, h.id)
	if g.targetProtectedLocked(h.target) {
		v.send = append(v.send, g.hideLocked(h)...)
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
	g.sessions[h.id] = &session{id: h.id, target: h.target, parent: h.parent, typ: h.typ, waiting: waiting}
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
	g.logf("cdp: detached an auto-attached session on a protected origin target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	var send [][]byte
	if h.waiting {
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
		if h == nil || h.parent != session || (!workerTypes[h.typ] && h.typ != "iframe") {
			continue
		}
		delete(g.held, sid)
		kind := "worker"
		if h.typ == "iframe" {
			kind = "iframe"
		}
		g.logf("cdp: releasing a held %s because %s target=%s session=%s parent=%s waiting=%t", kind, why, h.target, h.id, h.parent, h.waiting)
		send = append(send, g.hideLocked(h)...)
	}
	return send
}

func (g *guard) expireDetachLocked(h *heldSession) [][]byte {
	g.refuseHeldWaitLocked(h.target)
	g.noteHiddenLocked(h.id, h.target)
	g.logf("cdp: detached an auto-attached session that never got a URL target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	var send [][]byte
	if h.waiting {
		send = append(send, g.resumeLocked(h.id, h.target))
	}
	msg, _ := g.newCallLocked(h.parent, "Target.detachFromTarget", map[string]any{"sessionId": h.id}, false)
	return append(send, msg)
}

func (g *guard) expireHold(sessionID string) [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.held[sessionID]
	if !ok {
		return nil
	}
	group := g.emptyURLPopupGroupLocked(h)
	g.logf("cdp: an auto-attached target never got a URL target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	var send [][]byte
	for _, gh := range group {
		if gh == nil || g.held[gh.id] == nil {
			continue
		}
		delete(g.held, gh.id)
		send = append(send, g.expireDetachLocked(gh)...)
	}
	g.resolveUnknownLocked()
	return send
}

func (g *guard) dropHeldLocked(targetID string) {
	for sid, h := range g.held {
		if h.target == targetID {
			delete(g.held, sid)
			g.refuseHeldWaitLocked(h.target)
			g.noteHiddenLocked(sid, h.target)
		}
	}
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
		send = append(send, g.hideLocked(h)...)
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
