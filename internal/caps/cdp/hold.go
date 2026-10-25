package cdp

import (
	"encoding/json"
	"time"
)

const HoldTimeout = 10 * time.Second

type heldSession struct {
	id      string
	parent  string
	target  string
	typ     string
	params  map[string]any
	waiting bool
}

func (g *guard) holdLocked(parent string, params map[string]any, info targetInfo, waiting bool) [][]byte {
	sid, _ := params["sessionId"].(string)
	h := &heldSession{id: sid, parent: parent, target: info.TargetID, typ: info.Type, params: params, waiting: waiting}
	g.held[sid] = h
	g.logf("cdp: holding an auto-attached session until its target has a URL target=%s session=%s parent=%s waiting=%t", h.target, sid, parent, waiting)
	if !waiting {
		return nil
	}
	return [][]byte{g.resumeLocked(sid, info.TargetID)}
}

func (g *guard) releaseLocked(targetID string, info any) browserVerdict {
	var v browserVerdict
	for sid, h := range g.held {
		if h.target != targetID {
			continue
		}
		delete(g.held, sid)
		if g.targetProtectedLocked(targetID) {
			v.send = append(v.send, g.hideLocked(h)...)
			continue
		}
		g.logf("cdp: released an auto-attached session after its target got a URL target=%s session=%s parent=%s", targetID, sid, h.parent)
		g.sessions[sid] = &session{id: sid, target: targetID, parent: h.parent, typ: h.typ}
		v.send = append(v.send, g.touchLocked(targetID, h.typ)...)
		if obj, _ := info.(map[string]any); obj != nil {
			ti, _ := toInfo(obj)
			g.redactInfoLocked(obj, ti)
		}
		h.params["targetInfo"] = info
		h.params["waitingForDebugger"] = false
		ev := map[string]any{"method": "Target.attachedToTarget", "params": h.params}
		if h.parent != "" {
			ev["sessionId"] = h.parent
		}
		v.page = append(v.page, encode(ev))
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

func (g *guard) hideLocked(h *heldSession) [][]byte {
	g.hidden[h.id] = true
	g.logf("cdp: detached an auto-attached session on a protected origin target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
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
	delete(g.held, sessionID)
	g.logf("cdp: an auto-attached target never got a URL target=%s session=%s parent=%s waiting=%t", h.target, h.id, h.parent, h.waiting)
	send := g.hideLocked(h)
	g.resolveUnknownLocked()
	return send
}

func (g *guard) dropHeldLocked(targetID string) {
	for sid, h := range g.held {
		if h.target == targetID {
			delete(g.held, sid)
			g.hidden[sid] = true
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
