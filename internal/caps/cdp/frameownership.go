package cdp

import "encoding/json"

type frameOwnership struct {
	page     *target
	revision uint64
	session  string
	absent   bool
}

func (g *guard) bindIframeDocumentLocked(t *target) {
	if t.typ != "iframe" || t.framePage != nil {
		return
	}
	if page := g.iframePageLocked(t); page != nil {
		t.framePage, t.frameDocument = page, page.document
	}
}

func (g *guard) iframeDocumentInactiveLocked(t *target) bool {
	page := t.framePage
	if page == nil || page.gone {
		return false
	}
	if t.frameDocument != "" && page.document != "" {
		return t.frameDocument != page.document
	}
	owner := t.frameOwner
	nt := g.navTargets[page.id]
	return owner != nil && owner.page == page && owner.revision == page.documentRevision && nt != nil && !nt.failed && nt.session == owner.session && owner.absent
}

func (g *guard) rootDocumentLocked(page *target, loader string) {
	for _, t := range g.targets {
		g.bindIframeDocumentLocked(t)
	}
	page.document = loader
	page.documentRevision++
}

func (g *guard) probeIframeOwnersLocked() [][]byte {
	var send [][]byte
	for _, t := range g.targets {
		if t.gone || t.typ != "iframe" || (!t.protected && !g.o.Loaded(t.url) && !g.framesProtectedLocked(t.id)) {
			continue
		}
		g.bindIframeDocumentLocked(t)
		page := t.framePage
		if page == nil || page.gone || t.parentFrame != page.id || (t.frameDocument != "" && page.document != "") {
			continue
		}
		nt := g.navTargets[page.id]
		if nt == nil || nt.session == "" || nt.failed {
			continue
		}
		if owner := t.frameOwner; owner != nil && owner.page == page && owner.revision == page.documentRevision && owner.session == nt.session {
			continue
		}
		owner := &frameOwnership{page: page, revision: page.documentRevision, session: nt.session}
		t.frameOwner = owner
		send = append(send, g.callbackLocked(nt.session, "DOM.getFrameOwner", map[string]any{"frameId": t.id}, func(m message) [][]byte {
			g.frameOwnerAnsweredLocked(t, owner, nt, m)
			return nil
		}))
	}
	return send
}

func (g *guard) frameOwnerAnsweredLocked(t *target, owner *frameOwnership, nt *navTarget, m message) {
	page := owner.page
	if t.gone || page.gone || t.frameOwner != owner || page.documentRevision != owner.revision || g.navTargets[page.id] != nt || nt.session != owner.session || nt.failed {
		return
	}
	var result struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	var failure struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if m.Error == nil && json.Unmarshal(m.Result, &result) == nil && result.BackendNodeID > 0 {
		t.frameDocument = page.document
	} else if json.Unmarshal(m.Error, &failure) == nil && failure.Code == -32000 && (failure.Message == "Frame with the given id was not found." || failure.Message == "Frame with the given id does not belong to the target.") {
		owner.absent = true
	}
}
