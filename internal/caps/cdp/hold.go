package cdp

import (
	"encoding/json"
	"time"
)

const HoldTimeout = 10 * time.Second

type heldSession struct {
	id     string
	parent string
	target string
	typ    string
	params map[string]any
}

func (g *guard) holdLocked(parent string, params map[string]any, info targetInfo, waiting bool) [][]byte {
	sid, _ := params["sessionId"].(string)
	g.held[sid] = &heldSession{id: sid, parent: parent, target: info.TargetID, typ: info.Type, params: params}
	g.logf("cdp: holding an auto-attached session until its target has a URL")
	if !waiting {
		return nil
	}
	msg, _ := g.newCallLocked(sid, "Runtime.runIfWaitingForDebugger", nil, false)
	return [][]byte{msg}
}

func (g *guard) releaseLocked(targetID string, info any) browserVerdict {
	var v browserVerdict
	for sid, h := range g.held {
		if h.target != targetID {
			continue
		}
		delete(g.held, sid)
		if g.targetProtectedLocked(targetID) {
			v.send = append(v.send, g.hideLocked(h))
			continue
		}
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

func (g *guard) hideLocked(h *heldSession) []byte {
	g.hidden[h.id] = true
	g.logf("cdp: detached an auto-attached session on a protected origin")
	msg, _ := g.newCallLocked(h.parent, "Target.detachFromTarget", map[string]any{"sessionId": h.id}, false)
	return msg
}

func (g *guard) expireHold(sessionID string) [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.held[sessionID]
	if !ok {
		return nil
	}
	delete(g.held, sessionID)
	g.logf("cdp: an auto-attached target never got a URL")
	msg := g.hideLocked(h)
	g.resolveUnknownLocked()
	return [][]byte{msg}
}

func (g *guard) dropHeldLocked(targetID string) {
	for sid, h := range g.held {
		if h.target == targetID {
			delete(g.held, sid)
			g.hidden[sid] = true
		}
	}
}

func decodeParams(raw json.RawMessage) map[string]any {
	p, _ := decodeObject(raw)
	return p
}
