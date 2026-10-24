package cdp

import "encoding/json"

func (g *guard) frameOwnerLocked(frame, context string) *target {
	if frame == "" {
		return nil
	}
	if t := g.targets[frame]; t != nil && !t.gone && t.ctx == context && (t.typ == "page" || t.typ == "iframe") {
		return t
	}
	var owner *target
	for _, t := range g.targets {
		if t.gone || t.ctx != context || (t.typ != "page" && t.typ != "iframe") {
			continue
		}
		known := g.frames[t.id][frame]
		if s := g.shown[t.id]; s != nil {
			for _, c := range s.contexts {
				known = known || c.frame == frame
			}
		}
		if w := g.worlds[t.id]; w != nil {
			for _, c := range w.contexts {
				known = known || c.frame == frame
			}
		}
		if known {
			if owner != nil && owner != t {
				return nil
			}
			owner = t
		}
	}
	return owner
}

func (g *guard) iframePageLocked(t *target) *target {
	seen := map[string]bool{t.id: true}
	for t.typ == "iframe" {
		t = g.frameOwnerLocked(t.parentFrame, t.ctx)
		if t == nil || seen[t.id] {
			return nil
		}
		seen[t.id] = true
	}
	return t
}

func (g *guard) iframeProtectedLocked(id string) bool {
	page := g.targets[id]
	if page == nil || page.gone || page.typ != "page" {
		return false
	}
	for _, t := range g.targets {
		if t.gone || t.typ != "iframe" || t.ctx != page.ctx || (!t.protected && !g.o.Loaded(t.url) && !g.framesProtectedLocked(t.id)) {
			continue
		}
		parent := g.iframePageLocked(t)
		if parent == nil || parent == page {
			return true
		}
	}
	return false
}

func (g *guard) frameEventLocked(m *message) {
	if m.Method != "Page.frameAttached" && m.Method != "Page.frameDetached" && m.Method != "Page.frameNavigated" {
		return
	}
	id := g.worldTargetLocked(m.SessionID)
	if nt := g.navSessions[m.SessionID]; nt != nil {
		id = nt.target
	}
	if id == "" {
		return
	}
	var p struct {
		FrameID string `json:"frameId"`
		Frame   struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
		} `json:"frame"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	switch m.Method {
	case "Page.frameAttached":
		if p.FrameID == "" {
			return
		}
		if g.frames[id] == nil {
			g.frames[id] = map[string]bool{}
		}
		if len(g.frames[id]) < g.maxWorlds {
			g.frames[id][p.FrameID] = true
		}
	case "Page.frameDetached":
		delete(g.frames[id], p.FrameID)
	case "Page.frameNavigated":
		if p.Frame.ID == id && p.Frame.ParentID == "" {
			delete(g.frames, id)
		}
	}
}

func (g *guard) cutFrameCastsLocked() [][]byte {
	var send [][]byte
	for session := range g.casting {
		send = append(send, g.cutCastsLocked(g.worldTargetLocked(session))...)
	}
	return send
}
