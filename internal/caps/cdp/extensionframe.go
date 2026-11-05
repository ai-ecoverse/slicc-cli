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
	extensionWorldPrefix     = "slicc-ext-probe-"
	extensionObjectGroup     = "slicc-extension-frames"
	extensionShownProbe      = "function(){if(!this.isConnected)return true;if(this.checkVisibility({checkOpacity:false,visibilityProperty:true})){const box=this.getBoundingClientRect();if(box.width>0&&box.height>0)return true;}let node=this;for(let depth=0;depth<64;depth++){const root=node.getRootNode();if(root.activeElement!==node)return false;if(!root.host)return true;node=root.host;}return true;}"
	extensionShownPending    = "\x00extension-shown"
	extensionPaintHold       = 10 * time.Second
	extensionPaintHoldEnv    = "SLICC_CDP_EXTENSION_PAINT_HOLD"
	extensionFrameMarkerKey  = "sliccExtensionFrame"
	extensionFrameMarkerKind = "extensionFrame"
)

func extensionFrameMarker() map[string]any {
	return map[string]any{"version": 1, "kind": extensionFrameMarkerKind}
}

func (g *guard) extensionFrameOnPageLocked(frame, page string) bool {
	if frame == "" || page == "" {
		return false
	}
	for _, id := range g.extensionFrameIDsLocked(page) {
		if id == frame {
			return true
		}
	}
	return false
}

func (g *guard) trackedExtensionFrameIDsLocked() map[string]bool {
	set := map[string]bool{}
	for id, t := range g.targets {
		if g.extensionFrameTargetLocked(t) {
			set[id] = true
		}
	}
	for _, frames := range g.extensionFrameURLs {
		for frame := range frames {
			set[frame] = true
		}
	}
	return set
}

func (g *guard) forgetExtensionOwnerCallsLocked(session string) {
	for k := range g.extensionOwnerCalls {
		if session == "" || k.session == session {
			delete(g.extensionOwnerCalls, k)
		}
	}
}

func stripExtensionFrameMarkers(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t[extensionFrameMarkerKey]; ok {
			delete(t, extensionFrameMarkerKey)
			changed = true
		}
		for _, val := range t {
			if stripExtensionFrameMarkers(val) {
				changed = true
			}
		}
	case []any:
		for _, val := range t {
			if stripExtensionFrameMarkers(val) {
				changed = true
			}
		}
	}
	return changed
}

func frameOwnerNodeName(name string) bool {
	return name == "IFRAME" || name == "FRAME"
}

func (g *guard) takeExtensionOwnerCall(m map[string]any) bool {
	raw, ok := m["id"].(json.Number)
	if !ok {
		return false
	}
	id, err := raw.Int64()
	if err != nil {
		return false
	}
	session, _ := m["sessionId"].(string)
	k := callKey{session, id}
	if !g.extensionOwnerCalls[k] {
		return false
	}
	delete(g.extensionOwnerCalls, k)
	return true
}

func markExtensionFrameOwnerResult(m map[string]any) bool {
	res, ok := m["result"].(map[string]any)
	if !ok {
		return false
	}
	kept := map[string]any{extensionFrameMarkerKey: extensionFrameMarker()}
	if v, ok := res["backendNodeId"]; ok {
		kept["backendNodeId"] = v
	}
	if v, ok := res["nodeId"]; ok {
		kept["nodeId"] = v
	}
	m["result"] = kept
	return true
}

func markExtensionFrameOwnerNodes(v any, ids map[string]bool) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		name, _ := t["nodeName"].(string)
		if frameOwnerNodeName(name) {
			if frame, _ := t["frameId"].(string); ids[frame] {
				t[extensionFrameMarkerKey] = extensionFrameMarker()
				changed = true
			}
		}
		for _, val := range t {
			if markExtensionFrameOwnerNodes(val, ids) {
				changed = true
			}
		}
	case []any:
		for _, val := range t {
			if markExtensionFrameOwnerNodes(val, ids) {
				changed = true
			}
		}
	}
	return changed
}

func (g *guard) markExtensionFramePlacement(m map[string]any) bool {
	g.mu.Lock()
	ids := g.trackedExtensionFrameIDsLocked()
	ownerCall := g.takeExtensionOwnerCall(m)
	g.mu.Unlock()
	changed := false
	if ownerCall {
		changed = markExtensionFrameOwnerResult(m)
	}
	if len(ids) == 0 {
		return changed
	}
	return markExtensionFrameOwnerNodes(m, ids) || changed
}

func extensionPaintHoldDuration() time.Duration {
	return envDuration(extensionPaintHoldEnv, extensionPaintHold)
}

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

type extensionProbeWorld struct {
	name     string
	context  int64
	session  string
	frame    string
	document string
	revision uint64
}

type extensionWorldKey struct {
	session string
	frame   string
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

func (g *guard) cachedExtensionProbeWorldLocked(session, frame, document string, revision uint64) *extensionProbeWorld {
	if session == "" || frame == "" || document == "" || g.extensionProbeCache == nil {
		return nil
	}
	w := g.extensionProbeCache[extensionWorldKey{session, frame}]
	if w == nil || w.context == 0 || w.name == "" || w.session != session || w.frame != frame || w.document != document || w.revision != revision {
		return nil
	}
	return w
}

func (g *guard) storeExtensionProbeWorldLocked(session, frame, document string, revision uint64, name string, context int64) {
	if session == "" || frame == "" || document == "" || name == "" || context == 0 {
		return
	}
	if g.extensionProbeCache == nil {
		g.extensionProbeCache = map[extensionWorldKey]*extensionProbeWorld{}
	}
	key := extensionWorldKey{session, frame}
	if old := g.extensionProbeCache[key]; old != nil && old.name != "" && old.name != name {
		delete(g.extensionProbeWorlds, old.name)
	}
	g.extensionProbeCache[key] = &extensionProbeWorld{name: name, context: context, session: session, frame: frame, document: document, revision: revision}
}

func (g *guard) forgetExtensionProbeWorldLocked(w *extensionProbeWorld) {
	if w == nil {
		return
	}
	delete(g.extensionProbeWorlds, w.name)
	if g.extensionProbeCache != nil {
		delete(g.extensionProbeCache, extensionWorldKey{w.session, w.frame})
	}
}

func (g *guard) forgetExtensionProbeWorldSessionLocked(session string) {
	if session == "" {
		return
	}
	for key, w := range g.extensionProbeCache {
		if key.session == session || w.session == session {
			delete(g.extensionProbeWorlds, w.name)
			delete(g.extensionProbeCache, key)
		}
	}
}

func (g *guard) forgetExtensionProbeWorldsForTargetLocked(target string) {
	if target == "" {
		return
	}
	for id, s := range g.sessions {
		if s != nil && s.target == target {
			g.forgetExtensionProbeWorldSessionLocked(id)
		}
	}
}

func (g *guard) forgetExtensionProbeWorldContextLocked(session string, id int64) {
	if id == 0 || session == "" {
		return
	}
	for key, w := range g.extensionProbeCache {
		if w.context != id {
			continue
		}
		if w.session != session && key.session != session {
			continue
		}
		g.forgetExtensionProbeWorldLocked(w)
	}
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
	for _, h := range g.heldExtensionPaints {
		if h != nil && h.target == page {
			h.dirty = true
		}
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
	if g.heldExtensionPaints[k] != nil {
		g.logf("cdp: dropped a screenshot that finished while another extension-frame capture was still checking")
		return browserVerdict{forward: refusal(*m.ID, m.SessionID, protect.ShownReason())}
	}
	reservation, ok := g.holdReservation(data)
	if !ok {
		return browserVerdict{}
	}
	g.extensionPaintGen++
	gen := g.extensionPaintGen
	hold := extensionPaintHoldDuration()
	deadline := time.Now().Add(hold)
	g.logf("cdp: holding a screenshot for an extension-frame visibility check for %s", hold)
	if g.heldExtensionPaints == nil {
		g.heldExtensionPaints = map[callKey]*heldExtensionPaint{}
	}
	g.heldExtensionPaints[k] = &heldExtensionPaint{
		reservation: reservation,
		m:           *m,
		data:        data,
		session:     m.SessionID,
		target:      target,
		frames:      frames,
		deadline:    deadline,
		generation:  gen,
	}
	return browserVerdict{
		send: g.probeExtensionShownLocked(m.SessionID, target, frames, func(shown, ok bool) [][]byte {
			return g.finishExtensionPaintLocked(k, shown, ok)
		}),
		extensionPaints: []extensionPaintDeadline{{key: k, generation: gen, deadline: deadline}},
	}
}

func (g *guard) expireExtensionPaint(k callKey, gen uint64) (page [][]byte, releases []*link.Reservation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.heldExtensionPaints[k]
	if h == nil || h.generation != gen {
		return nil, nil
	}
	g.logf("cdp: dropped a rendering that stayed held past the extension-frame check deadline")
	g.pendingRelease = append(g.pendingRelease, h.reservation)
	delete(g.heldExtensionPaints, k)
	delete(g.extensionCaptureDirty, k)
	releases = g.takeReleasesLocked()
	if g.sessionRefusedLocked(k.session) {
		g.logf("cdp: extension-frame capture deadline skipped a refused session")
		return nil, releases
	}
	return [][]byte{refusal(k.id, k.session, protect.ShownReason())}, releases
}

func (g *guard) finishExtensionPaintLocked(k callKey, shown, ok bool) [][]byte {
	h := g.heldExtensionPaints[k]
	if h == nil || h.session != k.session || h.m.ID == nil || *h.m.ID != k.id {
		return nil
	}
	g.pendingRelease = append(g.pendingRelease, h.reservation)
	delete(g.heldExtensionPaints, k)
	dirty := h.dirty || g.extensionCaptureDirty[k]
	delete(g.extensionCaptureDirty, k)
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
	for k, h := range g.heldExtensionPaints {
		if h != nil {
			h.reservation.Release()
		}
		delete(g.heldExtensionPaints, k)
	}
}

func (g *guard) releaseExtensionPaintsForSessionLocked(session string) {
	if session == "" {
		return
	}
	for k, h := range g.heldExtensionPaints {
		if k.session != session {
			continue
		}
		if h != nil {
			g.pendingRelease = append(g.pendingRelease, h.reservation)
			if !g.sessionRefusedLocked(session) {
				g.pendingPage = append(g.pendingPage, refusal(k.id, k.session, protect.ShownReason()))
			}
		}
		delete(g.heldExtensionPaints, k)
	}
	for k := range g.extensionCaptureDirty {
		if k.session == session {
			delete(g.extensionCaptureDirty, k)
		}
	}
}

func (g *guard) markExtensionCaptureDirtyLocked(session string) {
	if session == "" {
		return
	}
	if g.extensionCaptureDirty == nil {
		g.extensionCaptureDirty = map[callKey]bool{}
	}
	for k, method := range g.paintMethods {
		if k.session == session && method == "Page.captureScreenshot" {
			g.extensionCaptureDirty[k] = true
		}
	}
	for k, h := range g.heldExtensionPaints {
		if h == nil || k.session != session {
			continue
		}
		h.dirty = true
		g.extensionCaptureDirty[k] = true
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
	session  string
	target   string
	frames   []string
	at       int
	world    string
	group    string
	document string
	revision uint64
	done     func(shown, ok bool) [][]byte
}

func (g *guard) extensionProbeDocumentLocked(target string) (string, uint64) {
	if t := g.targets[target]; t != nil {
		return t.document, t.documentRevision
	}
	return "", 0
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
	state.document, state.revision = g.extensionProbeDocumentLocked(state.target)
	if state.document == "" {
		return [][]byte{g.callbackLocked(state.session, "Page.getFrameTree", nil, func(tree message) [][]byte {
			return g.extensionFrameTreeAnsweredLocked(state, tree)
		})}
	}
	if cached := g.cachedExtensionProbeWorldLocked(state.session, parent, state.document, state.revision); cached != nil {
		state.world = cached.name
		return [][]byte{g.callbackLocked(state.session, "DOM.getFrameOwner", map[string]any{"frameId": frame}, func(owner message) [][]byte {
			return g.extensionOwnerAnsweredLocked(state, cached.context, owner)
		})}
	}
	return [][]byte{g.callbackLocked(state.session, "DOM.getDocument", map[string]any{"depth": 0}, func(m message) [][]byte {
		return g.extensionDocumentAnsweredLocked(state, frame, parent, m)
	})}
}

func (g *guard) extensionFrameTreeAnsweredLocked(state *extensionProbe, m message) [][]byte {
	if m.Error != nil {
		return state.done(true, false)
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID       string `json:"id"`
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if json.Unmarshal(m.Result, &tree) != nil || tree.FrameTree.Frame.LoaderID == "" {
		return state.done(true, false)
	}
	page := g.targets[state.target]
	if page == nil || page.gone {
		return state.done(true, false)
	}
	if id := tree.FrameTree.Frame.ID; id != "" && id != state.target {
		return state.done(true, false)
	}
	if page.document == "" {
		g.rootDocumentLocked(page, tree.FrameTree.Frame.LoaderID)
	} else if page.document != tree.FrameTree.Frame.LoaderID {
		return state.done(true, false)
	}
	return g.stepExtensionProbeLocked(state)
}

func (g *guard) extensionDocumentAnsweredLocked(state *extensionProbe, frame, parent string, m message) [][]byte {
	if m.Error != nil {
		return state.done(true, false)
	}
	document, revision := g.extensionProbeDocumentLocked(state.target)
	if document == "" || document != state.document || revision != state.revision {
		return state.done(true, false)
	}
	if cached := g.cachedExtensionProbeWorldLocked(state.session, parent, document, revision); cached != nil {
		state.world = cached.name
		return [][]byte{g.callbackLocked(state.session, "DOM.getFrameOwner", map[string]any{"frameId": frame}, func(owner message) [][]byte {
			return g.extensionOwnerAnsweredLocked(state, cached.context, owner)
		})}
	}
	state.world = g.newExtensionWorldNameLocked()
	state.document, state.revision = document, revision
	return [][]byte{g.callbackLocked(state.session, "Page.createIsolatedWorld", map[string]any{"frameId": parent, "worldName": state.world}, func(world message) [][]byte {
		return g.extensionWorldAnsweredLocked(state, frame, parent, world)
	})}
}

func (g *guard) extensionWorldAnsweredLocked(state *extensionProbe, frame, parent string, m message) [][]byte {
	var world struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &world) != nil || world.ExecutionContextID == 0 {
		return state.done(true, false)
	}
	document, revision := g.extensionProbeDocumentLocked(state.target)
	if document != state.document || revision != state.revision {
		return state.done(true, false)
	}
	g.storeExtensionProbeWorldLocked(state.session, parent, document, revision, state.world, world.ExecutionContextID)
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
