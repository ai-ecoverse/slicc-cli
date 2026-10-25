package cdp

import (
	"bytes"
	"encoding/json"
)

const maxDebuggerPauses = 64
const maxDebuggerPauseBytes = 1 << 20

type heldDebuggerPause struct {
	session string
	data    []byte
}

func (g *guard) expireDebuggerPause(h *heldDebuggerPause) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.debuggerPauses[h.session] != h {
		return nil
	}
	delete(g.debuggerPauses, h.session)
	if g.sessionRefusedLocked(h.session) {
		return nil
	}
	return g.resumeDebuggerLocked(h.session)
}

func (g *guard) resumeDebuggerLocked(session string) []byte {
	msg, _ := g.newCallLocked(session, "Debugger.resume", nil, false)
	return msg
}

func (g *guard) holdDebuggerPauseLocked(m *message, data []byte) browserVerdict {
	v := g.debuggerPausedEvent(m, data)
	if v.forward == nil {
		delete(g.debuggerPauses, m.SessionID)
		return v
	}
	if g.errorPageSessionLocked(m.SessionID) || len(data) > maxDebuggerPauseBytes || len(g.debuggerPauses) >= maxDebuggerPauses {
		delete(g.debuggerPauses, m.SessionID)
		return browserVerdict{send: [][]byte{g.resumeDebuggerLocked(m.SessionID)}}
	}
	h := &heldDebuggerPause{session: m.SessionID, data: bytes.Clone(data)}
	g.debuggerPauses[m.SessionID] = h
	return browserVerdict{debugger: []*heldDebuggerPause{h}}
}

func (g *guard) flushDebuggerPausesLocked(v *browserVerdict) {
	for session, held := range g.debuggerPauses {
		if g.sessionRefusedLocked(session) {
			delete(g.debuggerPauses, session)
			continue
		}
		s := g.sessions[session]
		nt := g.navTargets[s.target]
		if nt != nil && nt.confirm != nil && !nt.confirm.done && !nt.historyLocked {
			continue
		}
		delete(g.debuggerPauses, session)
		if nt == nil || nt.failed || nt.historyLocked || (nt.confirm != nil && nt.confirm.failed) {
			v.send = append(v.send, g.resumeDebuggerLocked(session))
			continue
		}
		var m message
		_ = json.Unmarshal(held.data, &m)
		pause := g.debuggerPausedEvent(&m, held.data)
		v.send = append(v.send, pause.send...)
		if pause.forward != nil {
			v.page = append(v.page, pause.forward)
		}
	}
}
