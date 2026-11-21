package cdp

import "time"

type docConfirm struct {
	target       string
	holds        map[string]*docHold
	ready        chan struct{}
	start        time.Time
	deadline     time.Time
	done         bool
	failed       bool
	latched      bool
	proofSession string
	settle       bool
	failedLoad   bool
}

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

func (g *guard) docConfirmWaitLocked(session string) <-chan struct{} {
	return nil
}

func (g *guard) pruneDocConfirmSessionLocked(session string) {}

func (g *guard) pruneDocConfirmTargetLocked(target string) {}

func (g *guard) expireDocConfirm(target string) {}

func (g *guard) refuseDocConfirmLocked(dc *docConfirm, latch bool) [][]byte {
	return nil
}

func (g *guard) docConfirmLiveProtectedDocumentLocked(url string) bool {
	return false
}

func (g *guard) docConfirmAttachEventLocked(m *message, data []byte, session string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	return browserVerdict{}, false
}

func (g *guard) holdDocConfirmReplyLocked(attached string, m *message, res map[string]any) (held bool, send [][]byte, page [][]byte) {
	return false, nil, nil
}

func (g *guard) docConfirmProofSessionLocked(session string) bool {
	return false
}

func (g *guard) docConfirmProofPendingLocked(target string) bool {
	return false
}

func (g *guard) settleDocConfirmHeldLocked(h *heldSession) browserVerdict {
	return g.admitHeldLocked(h)
}

func (g *guard) admitHeldLocked(h *heldSession) browserVerdict {
	var v browserVerdict
	if h == nil {
		return v
	}
	waiting := h.waiting
	if h.typ == "iframe" {
		waiting = h.waiting && !h.resumed
	}
	g.sessions[h.id] = &session{id: h.id, target: h.target, parent: h.parent, typ: h.typ, waiting: waiting, at: time.Now()}
	v.send = append(v.send, g.touchLocked(h.target, h.typ)...)
	if h.params == nil {
		h.params = map[string]any{}
	}
	h.params["waitingForDebugger"] = waiting
	ev := map[string]any{"method": "Target.attachedToTarget", "params": h.params}
	if h.parent != "" {
		ev["sessionId"] = h.parent
	}
	v.page = append(v.page, encode(ev))
	return v
}
