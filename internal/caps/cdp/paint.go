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
