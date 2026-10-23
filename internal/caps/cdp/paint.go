package cdp

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
	w := g.worlds[target]
	if w == nil {
		return false
	}
	if w.full {
		return true
	}
	for _, context := range w.contexts {
		if context.protectedFrame != "" {
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
	if len(g.casting) == 0 || !g.framesProtectedLocked(target) {
		return nil
	}
	var send [][]byte
	for id := range g.casting {
		if g.worldTargetLocked(id) != target {
			continue
		}
		delete(g.casting, id)
		g.castCut[id] = true
		g.logf("cdp: stopped a screencast on a target that now holds a protected frame")
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
	if g.castCut[m.SessionID] || g.framesProtectedLocked(g.worldTargetLocked(m.SessionID)) {
		return browserVerdict{}
	}
	return browserVerdict{forward: data}
}

func (g *guard) answerLocked(m *message, data []byte) browserVerdict {
	k := callKey{m.SessionID, *m.ID}
	paint := g.pending[k] == kindPaint
	if paint {
		g.pending[k] = kindPlain
	}
	v := g.responseLocked(m, data)
	if !paint || v.forward == nil || m.Error != nil || !g.framesProtectedLocked(g.worldTargetLocked(m.SessionID)) {
		return v
	}
	g.logf("cdp: dropped a rendering that finished after a protected frame appeared")
	v.forward = refusal(*m.ID, m.SessionID, refusedTarget)
	return v
}
