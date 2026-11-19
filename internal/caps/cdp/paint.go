package cdp

import "github.com/ai-ecoverse/slicc-cli/internal/protect"

var framePaintMethods = map[string]bool{
	"Page.captureScreenshot":          true,
	"Page.startScreencast":            true,
	"HeadlessExperimental.beginFrame": true,
	"Cast.startTabMirroring":          true,
	"LayerTree.makeSnapshot":          true,
	"LayerTree.loadSnapshot":          true,
	"LayerTree.replaySnapshot":        true,
	"LayerTree.profileSnapshot":       true,
	"LayerTree.snapshotCommandLog":    true,
}

func (g *guard) framesProtectedLocked(target string) bool {
	if g.iframeProtectedLocked(target) {
		return true
	}
	if g.unknownNonDefaultWorldProtectsLocked(target) {
		return true
	}
	w := g.worlds[target]
	if w == nil {
		return false
	}
	if w.full {
		return true
	}
	for _, context := range w.contexts {
		if context.protectedFrame != "" && !g.extensionFrameIDLocked(context.protectedFrame) {
			return true
		}
	}
	return false
}

var frameContentEventMethods = map[string]bool{
	"DOM.setChildNodes":          true,
	"DOM.characterDataModified":  true,
	"DOM.childNodeInserted":      true,
	"DOM.documentUpdated":        true,
	"Accessibility.nodesUpdated": true,
	"Accessibility.loadComplete": true,
}

func (g *guard) frameContentEventsBlockedLocked(session string) bool {
	target := g.worldTargetLocked(session)
	if target == "" {
		return false
	}
	if g.framesProtectedLocked(target) {
		return true
	}
	t := g.targets[target]
	if t == nil || t.gone {
		return g.frameUnderProtectedLocked(target)
	}
	if g.extensionFrameTargetLocked(t) {
		return false
	}
	if t.protected || g.targetLoadedLocked(t) || g.frameUnderProtectedLocked(target) {
		return true
	}
	return false
}

func (g *guard) frameContentEventDropped(m *message) bool {
	if m == nil || !frameContentEventMethods[m.Method] {
		return false
	}
	return g.frameContentEventsBlockedLocked(m.SessionID)
}

func (g *guard) noteDomainEnableLocked(c *command) {
	if c == nil || c.session == "" {
		return
	}
	switch c.method {
	case "DOM.enable":
		g.domEnabled[c.session] = true
	case "DOM.disable":
		delete(g.domEnabled, c.session)
	case "Accessibility.enable":
		g.axEnabled[c.session] = true
	case "Accessibility.disable":
		delete(g.axEnabled, c.session)
	}
}

func (g *guard) frameContentRebuildEventsLocked(target string) [][]byte {
	if target == "" {
		return nil
	}
	var page [][]byte
	for sid, s := range g.sessions {
		if s == nil || s.target != target {
			continue
		}
		if g.domEnabled[sid] {
			page = append(page, encode(map[string]any{"method": "DOM.documentUpdated", "sessionId": sid, "params": map[string]any{}}))
		}
		if g.axEnabled[sid] {
			page = append(page, encode(map[string]any{"method": "Accessibility.loadComplete", "sessionId": sid, "params": map[string]any{"root": map[string]any{"nodeId": "0"}}}))
		}
	}
	return page
}

func (g *guard) trackSoftLatchPageLocked(target string) {
	if target == "" {
		return
	}
	soft := g.unknownNonDefaultWorldProtectsLocked(target)
	was := g.softLatched[target]
	if soft {
		g.softLatched[target] = true
		return
	}
	if !was {
		return
	}
	delete(g.softLatched, target)
	if g.framesProtectedLocked(target) {
		return
	}
	g.contextSettlePage = append(g.contextSettlePage, g.frameContentRebuildEventsLocked(target)...)
}

func (g *guard) noteCastLocked(c *command) {
	switch c.method {
	case "Page.startScreencast":
		g.casting[c.session] = true
		delete(g.castCut, c.session)
	case "Page.stopScreencast":
		delete(g.casting, c.session)
	}
}

func (g *guard) cutCastsLocked(target string) [][]byte {
	if len(g.casting) == 0 || (!g.framesProtectedLocked(target) && !g.extensionFramesPresentLocked(target)) {
		return nil
	}
	var send [][]byte
	for id := range g.casting {
		if g.worldTargetLocked(id) != target {
			continue
		}
		delete(g.casting, id)
		g.castCut[id] = true
		g.logf("cdp: stopped a screencast on a target that now holds a protected or extension frame")
		msg, _ := g.newCallLocked(id, "Page.stopScreencast", nil, false)
		send = append(send, msg)
	}
	return send
}

func (g *guard) forgetCastLocked(id string) {
	delete(g.casting, id)
	delete(g.castCut, id)
}

func (g *guard) screencastFrameEvent(m *message, data []byte) browserVerdict {
	target := g.worldTargetLocked(m.SessionID)
	if g.castCut[m.SessionID] || g.framesProtectedLocked(target) || g.extensionFramesPresentLocked(target) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) answerLocked(m *message, data []byte) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	kind := g.pending[k]
	method := g.paintMethods[k]
	paint := kind == kindPaint || framePaintMethods[method]
	stream := kind == kindStreams
	if kind == kindPaint {
		g.pending[k] = kindPlain
	}
	v := g.responseLocked(m, data)
	delete(g.paintMethods, k)
	if v.forward == nil || m.Error != nil {
		return v
	}
	target := g.worldTargetLocked(m.SessionID)
	if method == "" {
		return v
	}
	if paint && g.framesProtectedLocked(target) {
		g.logf("cdp: dropped a rendering that finished after a protected frame appeared")
		v.forward = refusal(*m.ID, m.SessionID, refusedTarget)
		return v
	}
	if !g.extensionFramesPresentLocked(target) {
		delete(g.extensionCaptureDirty, k)
		return v
	}
	frames := g.extensionFrameIDsLocked(target)
	if g.extensionPresentPaintMethod(method) || protect.ExtensionPresentMethod(method) {
		g.logf("cdp: dropped a rendering that finished while an extension frame was present")
		v.forward = refusal(*m.ID, m.SessionID, protect.PresentReason())
		return v
	}
	if protect.ExtensionShownMethod(method) {
		if g.extensionCaptureDirty[k] {
			delete(g.extensionCaptureDirty, k)
			g.logf("cdp: dropped a rendering that sampled an extension frame as shown during capture")
			v.forward = refusal(*m.ID, m.SessionID, protect.ShownReason())
			return v
		}
		return g.holdExtensionPaintLocked(m, v.forward, target, frames)
	}
	if framePaintMethods[method] || stream && protect.ExtensionPresentMethod(method) {
		g.logf("cdp: dropped a rendering that finished while an extension frame was present")
		v.forward = refusal(*m.ID, m.SessionID, protect.PresentReason())
	}
	return v
}
