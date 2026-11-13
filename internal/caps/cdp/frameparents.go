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
		if g.extensionFrameTargetLocked(t) {
			continue
		}
		g.bindIframeDocumentLocked(t)
		if g.iframeDocumentInactiveLocked(t) {
			continue
		}
		parent := g.iframePageLocked(t)
		if parent == nil && t.framePage != nil && !t.framePage.gone {
			parent = t.framePage
		}
		if parent == nil || parent == page {
			return true
		}
	}
	return false
}

func (g *guard) frameEventLocked(m *message) [][]byte {
	if m.Method != "Page.frameAttached" && m.Method != "Page.frameDetached" && m.Method != "Page.frameNavigated" {
		return nil
	}
	id := g.worldTargetLocked(m.SessionID)
	if nt := g.navSessions[m.SessionID]; nt != nil {
		id = nt.target
	}
	if id == "" {
		return nil
	}
	var p struct {
		FrameID       string `json:"frameId"`
		ParentFrameID string `json:"parentFrameId"`
		Reason        string `json:"reason"`
		Frame         struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			LoaderID string `json:"loaderId"`
		} `json:"frame"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return nil
	}
	var send [][]byte
	switch m.Method {
	case "Page.frameAttached":
		if p.FrameID == "" {
			return nil
		}
		if g.frames[id] == nil {
			g.frames[id] = map[string]bool{}
		}
		if len(g.frames[id]) < g.maxWorlds {
			g.frames[id][p.FrameID] = true
		}
		parent := p.ParentFrameID
		if parent == "" {
			parent = id
		}
		part, _ := g.noteFrameParentLocked(p.FrameID, parent)
		send = append(send, part...)
		if t := g.targets[p.FrameID]; t != nil {
			t.frameOwner = nil
			t.frameDocument = ""
		}
	case "Page.frameDetached":
		delete(g.frames[id], p.FrameID)
		g.forgetExtensionFrameURLLocked(id, p.FrameID)
		if p.Reason == "remove" {
			g.forgetChildFrameLocked(p.FrameID)
			g.forgetFrameParentLocked(p.FrameID)
			g.clearBlockedChildrenLocked(p.FrameID)
		}
	case "Page.frameNavigated":
		g.clearBlockedChildrenLocked(p.Frame.ID)
		if p.Frame.ParentID != "" {
			part, _ := g.noteFrameParentLocked(p.Frame.ID, p.Frame.ParentID)
			send = append(send, part...)
		}
		if p.Frame.ID == id && p.Frame.ParentID == "" {
			if page := g.targets[id]; page != nil && page.typ == "page" {
				g.rootDocumentLocked(page, p.Frame.LoaderID)
			}
			delete(g.frames, id)
		}
	}
	return send
}

func (g *guard) cutFrameCastsLocked() [][]byte {
	var send [][]byte
	for session := range g.casting {
		send = append(send, g.cutCastsLocked(g.worldTargetLocked(session))...)
	}
	return send
}
