package cdp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	extensionWorldPrefix  = "slicc-ext-probe-"
	extensionObjectGroup  = "slicc-extension-frames"
	extensionShownProbe   = "function(){if(!this.isConnected)return true;if(this.checkVisibility({checkOpacity:false,visibilityProperty:true})){const box=this.getBoundingClientRect();if(box.width>0&&box.height>0)return true;}let node=this;for(let depth=0;depth<64;depth++){const root=node.getRootNode();if(root.activeElement!==node)return false;if(!root.host)return true;node=root.host;}return true;}"
	extensionShownPending = "\x00extension-shown"
	extensionPaintHold    = 10 * time.Second
)

type heldExtensionPaint struct {
	reservation *link.Reservation
	m           message
	data        []byte
	session     string
	target      string
	frames      []string
	deadline    time.Time
	dirty       bool
	generation  uint64
}

func (g *guard) newExtensionWorldNameLocked() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		g.nextID++
		return extensionWorldPrefix + itoa(g.nextID)
	}
	name := extensionWorldPrefix + hex.EncodeToString(b[:])
	if g.extensionProbeWorlds == nil {
		g.extensionProbeWorlds = map[string]bool{}
	}
	g.extensionProbeWorlds[name] = true
	return name
}

func (g *guard) extensionProbeWorldName(name string) bool {
	return strings.HasPrefix(name, extensionWorldPrefix) || g.extensionProbeWorlds[name]
}

func (g *guard) extensionFrameTargetLocked(t *target) bool {
	return t != nil && !t.gone && t.typ == "iframe" && g.o.ExtensionFrame(t.url)
}

func (g *guard) extensionFrameURLLocked(raw string) bool {
	return raw != "" && g.o.ExtensionFrame(raw)
}

func (g *guard) noteExtensionFrameURLLocked(page, frame, raw string) {
	if page == "" || frame == "" || !g.extensionFrameURLLocked(raw) {
		return
	}
	if g.extensionFrameURLs == nil {
		g.extensionFrameURLs = map[string]map[string]string{}
	}
	if g.extensionFrameURLs[page] == nil {
		g.extensionFrameURLs[page] = map[string]string{}
	}
	g.extensionFrameURLs[page][frame] = raw
	if h := g.heldExtensionPaint; h != nil && h.target == page {
		h.dirty = true
	}
}

func (g *guard) forgetExtensionFrameURLLocked(page, frame string) {
	if page == "" {
		delete(g.extensionFrameURLs, frame)
		for _, frames := range g.extensionFrameURLs {
			delete(frames, frame)
		}
		return
	}
	delete(g.extensionFrameURLs[page], frame)
	if len(g.extensionFrameURLs[page]) == 0 {
		delete(g.extensionFrameURLs, page)
	}
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

func (g *guard) extensionFrameIDsLocked(target string) []string {
	frames := g.extensionFramesUnderLocked(target)
	ids := make([]string, 0, len(frames))
	seen := map[string]bool{}
	for _, t := range frames {
		ids = append(ids, t.id)
		seen[t.id] = true
	}
	for frame := range g.extensionFrameURLs[target] {
		if seen[frame] {
			continue
		}
		ids = append(ids, frame)
		seen[frame] = true
	}
	return ids
}

func (g *guard) extensionFrameIDLocked(frame string) bool {
	if frame == "" {
		return false
	}
	if g.extensionFrameTargetLocked(g.targets[frame]) {
		return true
	}
	for _, frames := range g.extensionFrameURLs {
		if _, ok := frames[frame]; ok {
			return true
		}
	}
	return false
}

func (g *guard) extensionUnknownIFrameLocked(page string) bool {
	p := g.targets[page]
	if p == nil || p.gone || p.typ != "page" {
		return false
	}
	for _, t := range g.targets {
		if t.gone || t.typ != "iframe" || t.ctx != p.ctx {
			continue
		}
		if t.url == "" {
			return true
		}
	}
	return false
}

func (g *guard) extensionFramesPresentLocked(target string) bool {
	if len(g.extensionFramesUnderLocked(target)) > 0 {
		return true
	}
	if len(g.extensionFrameURLs[target]) > 0 {
		return true
	}
	return g.extensionUnknownIFrameLocked(target)
}

func (g *guard) extensionPresentPaintMethod(method string) bool {
	if protect.ExtensionPresentMethod(method) {
		return true
	}
	return framePaintMethods[method] && !protect.ExtensionShownMethod(method)
}

func (g *guard) extensionGateReasonLocked(c *command, target string) string {
	if !g.extensionFramesPresentLocked(target) {
		return ""
	}
	if g.extensionPresentPaintMethod(c.method) {
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
	g.extensionPaintGen++
	gen := g.extensionPaintGen
	g.heldExtensionPaint = &heldExtensionPaint{
		reservation: reservation,
		m:           *m,
		data:        data,
		session:     m.SessionID,
		target:      target,
		frames:      frames,
		deadline:    time.Now().Add(extensionPaintHold),
		generation:  gen,
	}
	g.scheduleExtensionPaintDeadline(gen)
	return browserVerdict{send: g.probeExtensionShownLocked(m.SessionID, target, frames, func(shown, ok bool) [][]byte {
		return g.finishExtensionPaintLocked(k, shown, ok)
	})}
}

func (g *guard) scheduleExtensionPaintDeadline(gen uint64) {
	time.AfterFunc(extensionPaintHold, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		h := g.heldExtensionPaint
		if h == nil || h.generation != gen {
			return
		}
		g.logf("cdp: dropped a rendering that stayed held past the extension-frame check deadline")
		g.pendingRelease = append(g.pendingRelease, h.reservation)
		g.pendingPage = append(g.pendingPage, refusal(*h.m.ID, h.session, protect.ShownReason()))
		g.heldExtensionPaint = nil
	})
}

func (g *guard) finishExtensionPaintLocked(k callKey, shown, ok bool) [][]byte {
	h := g.heldExtensionPaint
	if h == nil || h.session != k.session || h.m.ID == nil || *h.m.ID != k.id {
		return nil
	}
	g.pendingRelease = append(g.pendingRelease, h.reservation)
	g.heldExtensionPaint = nil
	dirty := h.dirty || g.extensionCaptureDirty[k.session]
	delete(g.extensionCaptureDirty, k.session)
	late := time.Now().After(h.deadline)
	if !ok || shown || dirty || late {
		switch {
		case shown:
			g.logf("cdp: dropped a rendering that finished while an extension frame was showing or focused")
		case dirty:
			g.logf("cdp: dropped a rendering that sampled an extension frame as shown during capture")
		case late:
			g.logf("cdp: dropped a rendering that stayed held past the extension-frame check deadline")
		default:
			g.logf("cdp: dropped a rendering because the extension-frame visibility probe failed")
		}
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

func (g *guard) releaseHeldExtensionPaintLocked() {
	h := g.heldExtensionPaint
	if h == nil {
		return
	}
	h.reservation.Release()
	g.heldExtensionPaint = nil
}

func (g *guard) markExtensionCaptureDirtyLocked(session string) {
	if session == "" {
		return
	}
	if g.extensionCaptureDirty == nil {
		g.extensionCaptureDirty = map[string]bool{}
	}
	g.extensionCaptureDirty[session] = true
	if h := g.heldExtensionPaint; h != nil && h.session == session {
		h.dirty = true
	}
}

func (g *guard) newExtensionObjectGroupLocked() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		g.nextID++
		return extensionObjectGroup + "-" + itoa(g.nextID)
	}
	return extensionObjectGroup + "-" + hex.EncodeToString(b[:])
}

func (g *guard) probeExtensionShownLocked(session, target string, frames []string, done func(shown, ok bool) [][]byte) [][]byte {
	if session == "" || len(frames) == 0 {
		return done(true, false)
	}
	state := &extensionProbe{session: session, target: target, frames: frames, at: 0, group: g.newExtensionObjectGroupLocked(), done: done}
	return g.stepExtensionProbeLocked(state)
}

type extensionProbe struct {
	session string
	target  string
	frames  []string
	at      int
	world   string
	group   string
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
	state.world = g.newExtensionWorldNameLocked()
	return [][]byte{g.callbackLocked(state.session, "Page.createIsolatedWorld", map[string]any{"frameId": parent, "worldName": state.world}, func(world message) [][]byte {
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
	return [][]byte{g.callbackLocked(state.session, "DOM.resolveNode", map[string]any{"backendNodeId": owner.BackendNodeID, "executionContextId": context, "objectGroup": state.group}, func(resolved message) [][]byte {
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
		return append(g.releaseExtensionGroupLocked(state.session, state.group), state.done(true, false)...)
	}
	return [][]byte{g.callbackLocked(state.session, "Runtime.callFunctionOn", map[string]any{"objectId": resolved.Object.ObjectID, "functionDeclaration": extensionShownProbe, "returnByValue": true}, func(call message) [][]byte {
		return g.extensionProbeAnsweredLocked(state, call)
	})}
}

func (g *guard) extensionProbeAnsweredLocked(state *extensionProbe, m message) [][]byte {
	send := g.releaseExtensionGroupLocked(state.session, state.group)
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

func (g *guard) releaseExtensionGroupLocked(session, group string) [][]byte {
	if group == "" {
		group = extensionObjectGroup
	}
	msg, _ := g.newCallLocked(session, "Runtime.releaseObjectGroup", map[string]any{"objectGroup": group}, false)
	return [][]byte{msg}
}
