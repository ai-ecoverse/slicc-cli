package cdp

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	extensionWorldName    = "slicc-extension-frames"
	extensionObjectGroup  = "slicc-extension-frames"
	extensionShownProbe   = "function(){if(!this.isConnected||this.checkVisibility({visibilityProperty:true}))return true;let node=this;for(let depth=0;depth<64;depth++){const root=node.getRootNode();if(root.activeElement!==node)return false;if(!root.host)return true;node=root.host;}return true;}"
	extensionShownPending = "\x00extension-shown"
)

type heldExtensionPaint struct {
	reservation *link.Reservation
	m           message
	data        []byte
	session     string
	target      string
	frames      []string
}

func (g *guard) extensionFrameTargetLocked(t *target) bool {
	return t != nil && !t.gone && t.typ == "iframe" && g.o.ExtensionFrame(t.url)
}

func (g *guard) extensionFramesUnderLocked(page string) []*target {
	p := g.targets[page]
	if p == nil || p.gone || p.typ != "page" {
		return nil
	}
	var out []*target
	for _, t := range g.targets {
		if !g.extensionFrameTargetLocked(t) || t.ctx != p.ctx {
			continue
		}
		g.bindIframeDocumentLocked(t)
		if g.iframeDocumentInactiveLocked(t) {
			continue
		}
		owner := g.iframePageLocked(t)
		if owner == nil && t.framePage != nil && !t.framePage.gone {
			owner = t.framePage
		}
		if owner == nil || owner == p {
			out = append(out, t)
		}
	}
	return out
}

func (g *guard) extensionFramesPresentLocked(target string) bool {
	return len(g.extensionFramesUnderLocked(target)) > 0
}

func (g *guard) extensionFrameIDsLocked(target string) []string {
	frames := g.extensionFramesUnderLocked(target)
	ids := make([]string, 0, len(frames))
	for _, t := range frames {
		ids = append(ids, t.id)
	}
	return ids
}

func (g *guard) extensionFrameIDLocked(frame string) bool {
	if frame == "" {
		return false
	}
	return g.extensionFrameTargetLocked(g.targets[frame])
}

func (g *guard) extensionGateReasonLocked(c *command, target string) string {
	if !g.extensionFramesPresentLocked(target) {
		return ""
	}
	if protect.ExtensionPresentMethod(c.method) {
		return protect.PresentReason()
	}
	if protect.ExtensionShownMethod(c.method) {
		if g.extensionShownCleared {
			return ""
		}
		return extensionShownPending
	}
	return ""
}

func (g *guard) needsExtensionShownProbeLocked(c *command) bool {
	t := g.sessionTargetLocked(c.session)
	if t == nil || !navTypes[t.typ] {
		return false
	}
	return protect.ExtensionShownMethod(c.method) && g.extensionFramesPresentLocked(t.id)
}

func (g *guard) afterExtensionShown(c *command, shown bool, ok bool) pageVerdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !ok || shown {
		g.logf("cdp: refused %s: %s", c.method, protect.ShownReason())
		return pageVerdict{reply: refusal(c.id, c.session, protect.ShownReason())}
	}
	g.extensionShownCleared = true
	defer func() { g.extensionShownCleared = false }()
	if reason := g.checkLocked(c); reason != "" {
		g.logf("cdp: refused %s: %s", c.method, reason)
		return pageVerdict{reply: refusal(c.id, c.session, reason)}
	}
	if gate := g.framesGateLocked(c.session); gate != nil {
		return pageVerdict{wait: gate}
	}
	return g.admitLocked(c)
}

func (g *guard) holdExtensionPaintLocked(m *message, data []byte, target string, frames []string) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	if g.heldExtensionPaint != nil {
		g.logf("cdp: dropped a screenshot that finished while another extension-frame capture was still checking")
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, protect.ShownReason())}
	}
	reservation, ok := g.holdReservation(data)
	if !ok {
		return browserVerdict{}
	}
	g.heldExtensionPaint = &heldExtensionPaint{reservation: reservation, m: *m, data: data, session: m.SessionID, target: target, frames: frames}
	return browserVerdict{send: g.probeExtensionShownLocked(m.SessionID, target, frames, func(shown, ok bool) [][]byte {
		return g.finishExtensionPaintLocked(k, shown, ok)
	})}
}

func (g *guard) finishExtensionPaintLocked(k callKey, shown, ok bool) [][]byte {
	h := g.heldExtensionPaint
	if h == nil || h.session != k.session || h.m.ID == nil || *h.m.ID != k.id {
		return nil
	}
	g.pendingRelease = append(g.pendingRelease, h.reservation)
	g.heldExtensionPaint = nil
	if !ok || shown {
		g.logf("cdp: dropped a rendering that finished after an extension frame was showing or focused")
		g.pendingPage = append(g.pendingPage, refusal(k.id, k.session, protect.ShownReason()))
		return nil
	}
	g.pendingPage = append(g.pendingPage, h.data)
	return nil
}

func (g *guard) takePendingPageLocked() [][]byte {
	out := g.pendingPage
	g.pendingPage = nil
	return out
}

func (g *guard) probeExtensionShownLocked(session, target string, frames []string, done func(shown, ok bool) [][]byte) [][]byte {
	if session == "" || len(frames) == 0 {
		return done(true, false)
	}
	state := &extensionProbe{session: session, target: target, frames: frames, at: 0, done: done}
	return g.stepExtensionProbeLocked(state)
}

type extensionProbe struct {
	session string
	target  string
	frames  []string
	at      int
	done    func(shown, ok bool) [][]byte
}

func (g *guard) stepExtensionProbeLocked(state *extensionProbe) [][]byte {
	if state.at >= len(state.frames) {
		return state.done(false, true)
	}
	frame := state.frames[state.at]
	t := g.targets[frame]
	parent := ""
	if t != nil {
		parent = t.parentFrame
	}
	if parent == "" {
		parent = state.target
	}
	return [][]byte{g.callbackLocked(state.session, "DOM.getDocument", map[string]any{"depth": 0}, func(m message) [][]byte {
		return g.extensionDocumentAnsweredLocked(state, frame, parent, m)
	})}
}

func (g *guard) extensionDocumentAnsweredLocked(state *extensionProbe, frame, parent string, m message) [][]byte {
	if m.Error != nil {
		return state.done(true, false)
	}
	return [][]byte{g.callbackLocked(state.session, "Page.createIsolatedWorld", map[string]any{"frameId": parent, "worldName": extensionWorldName}, func(world message) [][]byte {
		return g.extensionWorldAnsweredLocked(state, frame, world)
	})}
}

func (g *guard) extensionWorldAnsweredLocked(state *extensionProbe, frame string, m message) [][]byte {
	var world struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &world) != nil || world.ExecutionContextID == 0 {
		return state.done(true, false)
	}
	return [][]byte{g.callbackLocked(state.session, "DOM.getFrameOwner", map[string]any{"frameId": frame}, func(owner message) [][]byte {
		return g.extensionOwnerAnsweredLocked(state, world.ExecutionContextID, owner)
	})}
}

func (g *guard) extensionOwnerAnsweredLocked(state *extensionProbe, context int64, m message) [][]byte {
	var owner struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &owner) != nil || owner.BackendNodeID == 0 {
		return state.done(true, false)
	}
	return [][]byte{g.callbackLocked(state.session, "DOM.resolveNode", map[string]any{"backendNodeId": owner.BackendNodeID, "executionContextId": context, "objectGroup": extensionObjectGroup}, func(resolved message) [][]byte {
		return g.extensionResolvedAnsweredLocked(state, resolved)
	})}
}

func (g *guard) extensionResolvedAnsweredLocked(state *extensionProbe, m message) [][]byte {
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &resolved) != nil || resolved.Object.ObjectID == "" {
		return append(g.releaseExtensionGroupLocked(state.session), state.done(true, false)...)
	}
	return [][]byte{g.callbackLocked(state.session, "Runtime.callFunctionOn", map[string]any{"objectId": resolved.Object.ObjectID, "functionDeclaration": extensionShownProbe, "returnByValue": true}, func(call message) [][]byte {
		return g.extensionProbeAnsweredLocked(state, call)
	})}
}

func (g *guard) extensionProbeAnsweredLocked(state *extensionProbe, m message) [][]byte {
	send := g.releaseExtensionGroupLocked(state.session)
	var result struct {
		Result struct {
			Value *bool `json:"value"`
		} `json:"result"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.Result.Value == nil {
		return append(send, state.done(true, false)...)
	}
	if *result.Result.Value {
		return append(send, state.done(true, true)...)
	}
	state.at++
	return append(send, g.stepExtensionProbeLocked(state)...)
}

func (g *guard) releaseExtensionGroupLocked(session string) [][]byte {
	msg, _ := g.newCallLocked(session, "Runtime.releaseObjectGroup", map[string]any{"objectGroup": extensionObjectGroup}, false)
	return [][]byte{msg}
}
