package cdp

import (
	"encoding/json"
	"time"
)

const (
	ExtraHold        = 2 * time.Second
	maxHeldExtras    = 4096
	maxSettled       = 16384
	requestExtraInfo = "Network.requestWillBeSentExtraInfo"
)

type requestKey struct {
	session string
	request string
}

type heldExtra struct {
	m    message
	data []byte
}

func requestIDOf(raw json.RawMessage) string {
	var p struct {
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.RequestID
}

func (g *guard) extraReadyLocked(k requestKey) bool {
	return g.settled[k] || g.requestMarkedLocked(k.session, k.request)
}

func (g *guard) filterExtraLocked(m *message, data []byte, marked bool) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		if m.Method == requestExtraInfo {
			return true, g.requestExtra(p, marked), nil
		}
		return true, g.responseExtra(p, marked), nil
	})
}

func (g *guard) extraEvent(m *message, data []byte) browserVerdict {
	k := requestKey{m.SessionID, requestIDOf(m.Params)}
	if k.request == "" {
		return g.filterExtraLocked(m, data, true)
	}
	if g.extraReadyLocked(k) {
		return g.filterExtraLocked(m, data, g.requestMarkedLocked(k.session, k.request))
	}
	if g.heldExtraCount >= maxHeldExtras {
		g.logf("cdp: too many network extra-info events wait for their request; stripping one")
		return g.filterExtraLocked(m, data, true)
	}
	first := len(g.heldExtras[k]) == 0
	g.heldExtras[k] = append(g.heldExtras[k], heldExtra{m: *m, data: data})
	g.heldExtraCount++
	if !first {
		return browserVerdict{}
	}
	return browserVerdict{extra: []requestKey{k}}
}

func (g *guard) releaseExtrasLocked(k requestKey, expired bool) [][]byte {
	list := g.heldExtras[k]
	if len(list) == 0 {
		return nil
	}
	delete(g.heldExtras, k)
	g.heldExtraCount -= len(list)
	if g.sessionRefusedLocked(k.session) {
		return nil
	}
	if expired {
		g.logf("cdp: a network extra-info event never met its request; stripped it")
	}
	marked := expired || g.requestMarkedLocked(k.session, k.request)
	var out [][]byte
	for i := range list {
		if v := g.filterExtraLocked(&list[i].m, list[i].data, marked); v.forward != nil {
			out = append(out, v.forward)
		}
	}
	return out
}

func (g *guard) expireExtras(k requestKey) [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseExtrasLocked(k, true)
}

func (g *guard) settleLocked(k requestKey) {
	if g.settled[k] {
		return
	}
	g.settled[k] = true
	g.settledOrder = append(g.settledOrder, k)
	for len(g.settledOrder) > maxSettled {
		delete(g.settled, g.settledOrder[0])
		g.settledOrder = g.settledOrder[1:]
	}
}

func (g *guard) forgetExtrasLocked(session string) {
	for k, list := range g.heldExtras {
		if k.session == session {
			g.heldExtraCount -= len(list)
			delete(g.heldExtras, k)
		}
	}
	for k := range g.settled {
		if k.session == session {
			delete(g.settled, k)
		}
	}
}

func afterRequest(h eventHandler, settles bool) eventHandler {
	return func(g *guard, m *message, data []byte) browserVerdict {
		v := h(g, m, data)
		k := requestKey{m.SessionID, requestIDOf(m.Params)}
		if k.request == "" {
			return v
		}
		if settles {
			g.settleLocked(k)
		}
		if g.extraReadyLocked(k) {
			v.page = append(v.page, g.releaseExtrasLocked(k, false)...)
		}
		return v
	}
}

func forwardEvent(_ *guard, _ *message, data []byte) browserVerdict {
	return browserVerdict{forward: data}
}
