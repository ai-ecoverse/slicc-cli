package cdp

import "time"

func historyRestoreChildKind(typ string) bool {
	switch typ {
	case "iframe", "webview", "other", "background_page":
		return true
	}
	if !historyRestoreHoldWorkers() {
		return false
	}
	return workerTypes[typ]
}

func historyRestoreBrowserLevelKind(typ string) bool {
	switch typ {
	case "service_worker", "shared_worker", "background_page":
		return true
	}
	return false
}

func (g *guard) historyDocIdentityLocked(target string) docIdentity {
	id := docIdentity{frameID: target}
	if t := g.targets[target]; t != nil {
		id.url = t.url
	}
	if nt := g.navTargets[target]; nt != nil {
		if nt.documentURL != "" {
			id.url = nt.documentURL
		}
		if nt.loaderID != "" {
			id.loaderID = nt.loaderID
		}
	}
	return id
}

func (g *guard) historyLockedOwnerPageLocked(info targetInfo, parentSession string) (pageID string, nt *navTarget) {
	owner := g.sessionOwnerLocked(parentSession)
	if owner == nil {
		for _, id := range []string{info.ParentFrameID, info.ParentID, info.OpenerID, info.OpenerFrameID} {
			if p := g.targets[id]; p != nil && !p.gone {
				owner = p
				break
			}
		}
	}
	if owner == nil {
		return "", nil
	}
	page := owner
	switch owner.typ {
	case "iframe":
		if root := g.iframePageLocked(owner); root != nil {
			page = root
		}
	case "tab":
		if under := g.pageUnderTabLocked(owner); under != nil {
			page = under
		}
	}
	if !navTypes[page.typ] && page.typ != "page" {
		return "", nil
	}
	nt = g.navTargets[page.id]
	if nt == nil {
		return "", nil
	}
	if historyRestoreRequireHistoryLocked() && !nt.historyLocked {
		return "", nil
	}
	return page.id, nt
}

func (g *guard) streamHasHistoryConcernLocked() bool {
	for _, cand := range g.navTargets {
		if cand == nil || cand.failed {
			continue
		}
		if cand.historyLocked {
			return true
		}
		if !historyRestoreUnresolvedNeedsLockedStream() && !cand.done {
			return true
		}
	}
	return false
}

func (g *guard) streamHistoryConcernPageLocked() (pageID string, nt *navTarget) {
	if !historyRestorePinConcernPage() {
		return "", nil
	}
	for id, cand := range g.navTargets {
		if cand == nil || cand.failed {
			continue
		}
		if cand.historyLocked {
			return id, cand
		}
		if !historyRestoreUnresolvedNeedsLockedStream() && !cand.done {
			return id, cand
		}
	}
	return "", nil
}

func (g *guard) historyOwnerFromIDsLocked(ids ...string) *target {
	for _, id := range ids {
		if p := g.targets[id]; p != nil && !p.gone {
			return p
		}
	}
	return nil
}

func (g *guard) historyOwnerTargetLocked(info targetInfo, parentSession string) *target {
	t := g.targets[info.TargetID]
	if t != nil && workerTypes[t.typ] {
		if owner := g.historyOwnerFromIDsLocked(t.openerFrame, t.parentFrame, t.opener); owner != nil {
			return owner
		}
		if owner := g.historyOwnerFromIDsLocked(info.OpenerFrameID, info.ParentFrameID, info.ParentID, info.OpenerID); owner != nil {
			return owner
		}
	}
	if !historyRestoreBrowserLevelKind(info.Type) {
		if owner := g.sessionOwnerLocked(parentSession); owner != nil {
			return owner
		}
	}
	if owner := g.historyOwnerFromIDsLocked(info.ParentFrameID, info.ParentID, info.OpenerID, info.OpenerFrameID); owner != nil {
		return owner
	}
	if t == nil {
		return nil
	}
	if t.typ == "iframe" {
		if parent := g.iframeParentTargetLocked(t); parent != nil {
			return parent
		}
	}
	return g.historyOwnerFromIDsLocked(t.opener, t.openerFrame, t.parentFrame)
}

func (g *guard) historyPageNavOfOwnerLocked(owner *target) (pageID string, nt *navTarget) {
	if owner == nil {
		return "", nil
	}
	page := owner
	switch owner.typ {
	case "iframe":
		if root := g.iframePageLocked(owner); root != nil {
			page = root
		}
	case "tab":
		if under := g.pageUnderTabLocked(owner); under != nil {
			page = under
		}
	}
	if !navTypes[page.typ] && page.typ != "page" {
		return "", nil
	}
	nt = g.navTargets[page.id]
	if nt == nil {
		return "", nil
	}
	return page.id, nt
}

func (g *guard) historyOwnerPageAnyLocked(info targetInfo, parentSession string) (pageID string, nt *navTarget) {
	return g.historyPageNavOfOwnerLocked(g.historyOwnerTargetLocked(info, parentSession))
}

func (g *guard) stampHistoryHoldAttachSeqLocked(h *heldSession) {
	if h == nil {
		return
	}
	if h.historyAttachMinSeq == 0 {
		h.historyAttachMinSeq = g.confirmSeq + 1
	}
}

func (g *guard) stampHistoryHoldConfirmSeqLocked(h *heldSession) {
	if h == nil {
		return
	}
	g.stampHistoryHoldAttachSeqLocked(h)
	h.historyConfirmMinSeq = h.historyAttachMinSeq
}

func (g *guard) markHistoryUnresolvedHoldLocked(h *heldSession, pageID, typ, sid string) {
	if h == nil {
		return
	}
	h.historyUnresolved = true
	h.historySnap = docIdentity{}
	g.stampHistoryHoldConfirmSeqLocked(h)
	if historyRestorePinConcernPage() {
		h.historyParent = pageID
	} else {
		h.historyParent = ""
	}
	g.logf("cdp: holding unresolved-owner child kind=%s session=%s attachSeq=%d", typ, sid, h.historyAttachMinSeq)
}

func historyHoldSecondReads(h *heldSession) int {
	if h == nil || len(h.historyReadSeqs) <= 1 {
		return 0
	}
	return len(h.historyReadSeqs) - 1
}

func (g *guard) noteHistoryHoldConfirmIssuedLocked(parent string, issued int64) {
	if parent == "" || issued <= 0 {
		return
	}
	for _, h := range g.held {
		if h == nil || h.historyParent != parent || h.historyUnresolved {
			continue
		}
		if h.historyConfirmMinSeq <= 0 || h.historyConfirmMinSeq > issued {
			continue
		}
		n := len(h.historyReadSeqs)
		if n > 0 && h.historyReadSeqs[n-1] == issued {
			continue
		}
		h.historyReadSeqs = append(h.historyReadSeqs, issued)
	}
}

func (g *guard) pageHasHistoryChildrenLocked(page string) bool {
	if page == "" {
		return false
	}
	for _, s := range g.sessions {
		if s == nil || s.refused {
			continue
		}
		if g.childBelongsToPageLocked(s.target, page) {
			return true
		}
	}
	for _, h := range g.held {
		if h == nil {
			continue
		}
		if h.historyParent == page || g.childBelongsToPageLocked(h.target, page) {
			return true
		}
	}
	for _, t := range g.targets {
		if t == nil || t.gone || t.id == page || !historyRestoreChildKind(t.typ) {
			continue
		}
		if g.childBelongsToPageLocked(t.id, page) {
			return true
		}
	}
	return false
}

func (g *guard) childBelongsToPageLocked(child, page string) bool {
	if child == "" || page == "" || child == page {
		return false
	}
	t := g.targets[child]
	if t == nil {
		return false
	}
	if t.typ == "iframe" {
		if p := g.iframePageLocked(t); p != nil {
			return p.id == page
		}
	}
	for _, id := range []string{t.opener, t.openerFrame, t.parentFrame} {
		if id == page {
			return true
		}
	}
	return false
}

func (g *guard) historyUnresolvedAttachEligibleLocked(info targetInfo, parentSession string) (pageID string, nt *navTarget, hold bool) {
	anyID, anyNT := g.historyOwnerPageAnyLocked(info, parentSession)
	if anyID != "" && anyNT != nil {
		if anyNT.done && !anyNT.historyLocked {
			return "", nil, false
		}
		if !anyNT.done {
			if historyRestoreBrowserLevelKind(info.Type) {
				return "", nil, false
			}
			if historyRestoreOwnOwnerUndecidedNeedsLockedStream() && !g.streamHasHistoryConcernLocked() {
				return "", nil, false
			}
			return anyID, anyNT, true
		}
	}
	if historyRestoreBrowserLevelKind(info.Type) {
		return "", nil, false
	}
	if !historyRestoreHoldUnresolvedOwner() || !g.streamHasHistoryConcernLocked() {
		return "", nil, false
	}
	if historyRestorePinConcernPage() {
		pageID, nt = g.streamHistoryConcernPageLocked()
		if pageID == "" || nt == nil {
			return "", nil, false
		}
		return pageID, nt, true
	}
	return "", nil, true
}

func (g *guard) historyHoldAttachLocked(m *message, pSession string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	if !historyRestoreHoldChildren() || !historyRestoreChildKind(info.Type) {
		return browserVerdict{}, false
	}
	pageID, nt := g.historyLockedOwnerPageLocked(info, m.SessionID)
	unresolved := pageID == "" || nt == nil
	if unresolved {
		if anyID, anyNT := g.historyOwnerPageAnyLocked(info, m.SessionID); anyID != "" && anyNT != nil && anyNT.historyLocked && anyNT.done {
			pageID, nt, unresolved = anyID, anyNT, false
		} else {
			var hold bool
			pageID, nt, hold = g.historyUnresolvedAttachEligibleLocked(info, m.SessionID)
			if !hold {
				return browserVerdict{}, false
			}
		}
	}
	params := g.attachedHoldParams(m, pSession, waiting, info)
	holdSend := g.holdLocked(m.SessionID, params, info, waiting)
	if h := g.held[pSession]; h != nil {
		if unresolved {
			g.markHistoryUnresolvedHoldLocked(h, pageID, info.Type, pSession)
		} else {
			h.historyParent = pageID
			h.historySnap = g.historyDocIdentityLocked(pageID)
			g.stampHistoryHoldConfirmSeqLocked(h)
			g.logf("cdp: holding a child of a history-locked page until its document is confirmed target=%s session=%s parent=%s attachSeq=%d", info.TargetID, pSession, pageID, h.historyAttachMinSeq)
		}
	}
	if unresolved {
		return browserVerdict{send: append(send, holdSend...), hold: []string{pSession}}, true
	}
	if !historyRestoreWaitForConfirm() {
		if h := g.held[pSession]; h != nil {
			part := g.deliverHistoryHoldLocked(h, true, 0)
			return browserVerdict{send: append(append(send, holdSend...), part.send...), page: part.page, hold: []string{pSession}}, true
		}
	}
	ask := g.beginHistoryParentConfirmLocked(nt)
	return browserVerdict{send: append(append(send, holdSend...), ask...), hold: []string{pSession}}, true
}

func (g *guard) beginHistoryParentConfirmLocked(nt *navTarget) [][]byte {
	if nt == nil {
		return nil
	}
	g.beginConfirmLocked(nt)
	return g.confirmTargetLocked(nt)
}

func (g *guard) resolveHistoryOwnerPageLocked(t *target, parentSession string) (pageID string, nt *navTarget) {
	if t == nil {
		return "", nil
	}
	info := targetInfo{TargetID: t.id, Type: t.typ, URL: t.url, ParentFrameID: t.parentFrame, ParentID: t.parentFrame, OpenerID: t.opener, OpenerFrameID: t.openerFrame}
	if pageID, nt = g.historyLockedOwnerPageLocked(info, parentSession); nt != nil {
		return pageID, nt
	}
	if p := g.iframePageLocked(t); p != nil {
		nt = g.navTargets[p.id]
		if nt != nil && (!historyRestoreRequireHistoryLocked() || nt.historyLocked) {
			return p.id, nt
		}
	}
	for _, id := range []string{t.opener, t.openerFrame, t.parentFrame} {
		if p := g.targets[id]; p != nil && navTypes[p.typ] {
			cand := p
			if p.typ == "iframe" {
				if root := g.iframePageLocked(p); root != nil {
					cand = root
				}
			}
			nt = g.navTargets[cand.id]
			if nt != nil && (!historyRestoreRequireHistoryLocked() || nt.historyLocked) {
				return cand.id, nt
			}
		}
	}
	return "", nil
}

func (g *guard) historyExplicitOwnerLocked(h *heldSession, t *target, parentSession string) (pageID string, nt *navTarget, unresolved, skip bool) {
	if h != nil && h.historyParent != "" && !h.historyUnresolved {
		pageID, nt = h.historyParent, g.navTargets[h.historyParent]
	}
	if nt == nil {
		pageID, nt = g.resolveHistoryOwnerPageLocked(t, parentSession)
	}
	if pageID != "" && nt != nil {
		return pageID, nt, false, false
	}
	info := targetInfo{TargetID: t.id, Type: t.typ, URL: t.url, ParentFrameID: t.parentFrame, ParentID: t.parentFrame, OpenerID: t.opener, OpenerFrameID: t.openerFrame}
	pageID, nt, hold := g.historyUnresolvedAttachEligibleLocked(info, parentSession)
	if !hold {
		return "", nil, true, true
	}
	return pageID, nt, true, false
}

func (g *guard) parkHistoryAttachReplyLocked(h *heldSession, m *message, res map[string]any) {
	h.hasReply = true
	h.replyID = *m.ID
	h.replySid = m.SessionID
	wrapped := map[string]any{"id": *m.ID, "result": res}
	if m.SessionID != "" {
		wrapped["sessionId"] = m.SessionID
	}
	h.reply = encode(wrapped)
}

func (g *guard) ensureHistoryExplicitHoldLocked(sid, parent, attached, typ string) *heldSession {
	h := g.held[sid]
	if h != nil {
		return h
	}
	h = &heldSession{id: sid, parent: parent, target: attached, typ: typ, at: time.Now()}
	g.held[sid] = h
	return h
}

func (g *guard) holdHistoryAttachReplyLocked(attached string, m *message, res map[string]any) (held bool, send [][]byte, page [][]byte) {
	if !historyRestoreHoldExplicitAttach() || !historyRestoreHoldChildren() {
		return false, nil, nil
	}
	sid, _ := res["sessionId"].(string)
	if sid == "" || sid == g.relaySession || m.ID == nil {
		return false, nil, nil
	}
	t := g.targets[attached]
	if t == nil || !historyRestoreChildKind(t.typ) {
		return false, nil, nil
	}
	pageID, nt, unresolved, skip := g.historyExplicitOwnerLocked(g.held[sid], t, m.SessionID)
	if skip {
		return false, nil, nil
	}
	h := g.ensureHistoryExplicitHoldLocked(sid, m.SessionID, attached, t.typ)
	if unresolved || h.historyUnresolved {
		g.markHistoryUnresolvedHoldLocked(h, pageID, t.typ, sid)
	} else {
		h.historyParent = pageID
		if h.historySnap.frameID == "" && h.historySnap.url == "" {
			h.historySnap = g.historyDocIdentityLocked(pageID)
		}
		g.stampHistoryHoldConfirmSeqLocked(h)
		g.logf("cdp: holding an explicit attach of a child of a history-locked page until its document is confirmed target=%s session=%s parent=%s attachSeq=%d", attached, sid, pageID, h.historyAttachMinSeq)
	}
	g.parkHistoryAttachReplyLocked(h, m, res)
	if unresolved {
		return true, nil, g.takePendingPageLocked()
	}
	if !historyRestoreWaitForConfirm() {
		part := g.deliverHistoryHoldLocked(h, true, 0)
		return true, part.send, append(part.page, g.takeAnsweredHeldLocked()...)
	}
	ask := g.beginHistoryParentConfirmLocked(nt)
	return true, ask, g.takePendingPageLocked()
}

func historyIdentityFieldOK(snap, cur string) bool {
	if snap == "" || cur == "" {
		return !historyRestoreEmptySnapHides()
	}
	return snap == cur
}

func (g *guard) stampHistoryHoldReadIdentityLocked(parent string, frame confirmFrame) {
	if !historyRestoreIdentityFromRead() || parent == "" {
		return
	}
	read := docIdentity{frameID: frame.ID, loaderID: frame.LoaderID, url: frame.URL}
	for _, h := range g.held {
		if h == nil || h.historyParent != parent || h.historyUnresolved {
			continue
		}
		if !historyRestoreIdentityRestampOnRead() && h.historyReadStamped {
			continue
		}
		h.historySnap = read
		h.historyReadStamped = true
	}
}

func (g *guard) historyHoldIdentityOKLocked(parent string, snap docIdentity) bool {
	nt := g.navTargets[parent]
	t := g.targets[parent]
	if nt == nil || !nt.done || nt.failed || t == nil || t.protected || t.gone {
		return false
	}
	if g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.securityOrigin) || g.o.Loaded(t.url) {
		return false
	}
	if !historyRestoreIdentityCompare() {
		return true
	}
	cur := g.historyDocIdentityLocked(parent)
	if historyRestoreIdentityFromRead() {
		if snap.loaderID == "" || snap.url == "" || snap.frameID == "" {
			return false
		}
		return snap.loaderID == cur.loaderID && snap.url == cur.url && snap.frameID == cur.frameID
	}
	return historyIdentityFieldOK(snap.loaderID, cur.loaderID) &&
		historyIdentityFieldOK(snap.url, cur.url) &&
		historyIdentityFieldOK(snap.frameID, cur.frameID)
}

func (g *guard) answerHistoryHeldReplyLocked(h *heldSession) {
	if h == nil || !h.hasReply || !historyRestoreAnswerDetachReply() {
		return
	}
	g.answerHeldLocked(refusal(h.replyID, h.replySid, refusedTarget))
	h.hasReply = false
	h.reply = nil
}

func (g *guard) settleUnresolvedHistoryHoldsLocked() browserVerdict {
	var v browserVerdict
	ids := make([]string, 0, len(g.held))
	for sid, h := range g.held {
		if h != nil && h.historyUnresolved {
			ids = append(ids, sid)
		}
	}
	for _, sid := range ids {
		h := g.held[sid]
		if h == nil {
			continue
		}
		part := g.tryResolveUnresolvedHistoryHoldLocked(h)
		v.send = append(v.send, part.send...)
		v.page = append(v.page, part.page...)
	}
	return v
}

func (g *guard) tryResolveUnresolvedHistoryHoldLocked(h *heldSession) browserVerdict {
	if h == nil || !h.historyUnresolved {
		return browserVerdict{}
	}
	if historyRestorePinConcernPage() {
		return browserVerdict{}
	}
	t := g.targets[h.target]
	if t == nil {
		return browserVerdict{}
	}
	info := targetInfo{TargetID: t.id, Type: t.typ, URL: t.url, ParentFrameID: t.parentFrame, ParentID: t.parentFrame, OpenerID: t.opener, OpenerFrameID: t.openerFrame}
	if h.params != nil {
		if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
			if ti, _ := toInfo(obj); ti.TargetID != "" {
				info = ti
			}
		}
	}
	pageID, nt := g.historyOwnerPageAnyLocked(info, h.parent)
	if pageID == "" || nt == nil {
		return browserVerdict{}
	}
	waited := time.Since(h.at).Milliseconds()
	if !nt.done {
		if !historyRestoreUnresolvedAwaitDecide() {
			return browserVerdict{}
		}
		h.historyParent = pageID
		g.logf("cdp: unresolved-owner child awaiting owner decision kind=%s session=%s waitedMs=%d", h.typ, h.id, waited)
		return browserVerdict{}
	}
	if nt.historyLocked {
		if !historyRestoreUnresolvedToLocked() {
			return browserVerdict{}
		}
		h.historyUnresolved = false
		h.historyParent = pageID
		h.historySnap = g.historyDocIdentityLocked(pageID)
		g.stampHistoryHoldConfirmSeqLocked(h)
		g.logf("cdp: unresolved-owner child resolved to history-locked page kind=%s session=%s waitedMs=%d attachSeq=%d", h.typ, h.id, waited, h.historyAttachMinSeq)
		if !historyRestoreWaitForConfirm() {
			return g.deliverHistoryHoldLocked(h, true, 0)
		}
		if !historyRestoreRejectDoneConfirm() && nt.confirm != nil && nt.confirm.done {
			return g.deliverHistoryHoldLocked(h, !nt.confirm.failed, nt.confirm.issuedSeq)
		}
		return browserVerdict{send: g.beginHistoryParentConfirmLocked(nt)}
	}
	if !historyRestoreUnresolvedToOrdinary() {
		return browserVerdict{}
	}
	g.logf("cdp: unresolved-owner child resolved to ordinary page kind=%s session=%s waitedMs=%d", h.typ, h.id, waited)
	return g.releaseUnresolvedToOrdinaryLocked(h)
}

func (g *guard) historyHeldInfoLocked(h *heldSession) targetInfo {
	info := targetInfo{TargetID: h.target, Type: h.typ}
	if t := g.targets[h.target]; t != nil {
		info.URL, info.ParentFrameID, info.ParentID, info.OpenerID, info.OpenerFrameID = t.url, t.parentFrame, t.parentFrame, t.opener, t.openerFrame
	}
	if h.params != nil {
		if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
			if ti, _ := toInfo(obj); ti.TargetID != "" {
				return ti
			}
		}
	}
	return info
}

func (g *guard) answerParkedHistoryReplyLocked(sid string, reply []byte, replyID int64, replySid string, hasReply bool) [][]byte {
	if !hasReply || len(reply) == 0 {
		return nil
	}
	if attachPassRefuseCut() && (g.hidden[sid] || g.refused[sid]) {
		g.answerHeldLocked(refusal(replyID, replySid, refusedTarget))
	} else {
		g.answerHeldLocked(reply)
	}
	return g.takeAnsweredHeldLocked()
}

func (g *guard) redecideHeldTargetLocked(target string) {
	t := g.targets[target]
	if t == nil {
		return
	}
	prev := t.protected
	g.targetVerdictLocked(t)
	t.protected = g.decide(t, prev)
}

func (g *guard) releaseUnresolvedToOrdinaryLocked(h *heldSession) browserVerdict {
	if h == nil {
		return browserVerdict{}
	}
	reply, replyID, replySid, hasReply := h.reply, h.replyID, h.replySid, h.hasReply
	parent, waiting, params := h.parent, h.waiting, h.params
	info := g.historyHeldInfoLocked(h)
	sid := h.id
	h.historyUnresolved = false
	h.historyParent = ""
	h.hasReply = false
	h.reply = nil
	if historyRestoreUnresolvedOrdinaryBypass() {
		part := g.deliverHeldLocked(h)
		part.page = append(part.page, g.answerParkedHistoryReplyLocked(sid, reply, replyID, replySid, hasReply)...)
		return part
	}
	delete(g.held, sid)
	g.redecideHeldTargetLocked(info.TargetID)
	m := &message{SessionID: parent, Method: "Target.attachedToTarget", Params: encode(map[string]any{"sessionId": sid, "waitingForDebugger": waiting, "targetInfo": map[string]any{"targetId": info.TargetID, "type": info.Type, "url": info.URL, "openerId": info.OpenerID, "openerFrameId": info.OpenerFrameID, "parentFrameId": info.ParentFrameID, "parentId": info.ParentID}})}
	v, handled := g.attachedHideOrHoldDecisionLocked(m, sid, waiting, info, nil, false)
	if handled {
		if nh := g.held[sid]; nh != nil && hasReply {
			nh.hasReply, nh.replyID, nh.replySid, nh.reply = true, replyID, replySid, reply
		} else {
			v.page = append(v.page, g.answerParkedHistoryReplyLocked(sid, reply, replyID, replySid, hasReply)...)
		}
		return v
	}
	h.params = params
	g.held[sid] = h
	part := g.deliverHeldLocked(h)
	part.page = append(part.page, g.answerParkedHistoryReplyLocked(sid, reply, replyID, replySid, hasReply)...)
	return part
}

func (g *guard) historyHoldFreshEnoughLocked(h *heldSession, readSeq int64) bool {
	if h == nil || !historyRestoreConfirmFreshness() || readSeq <= 0 || h.historyAttachMinSeq <= 0 {
		return true
	}
	return readSeq >= h.historyAttachMinSeq
}

func (g *guard) ensureHistoryHoldParamsLocked(h *heldSession) {
	if h == nil || h.params != nil {
		return
	}
	h.params = map[string]any{"sessionId": h.id, "waitingForDebugger": h.waiting, "targetInfo": map[string]any{"targetId": h.target, "type": h.typ}}
	t := g.targets[h.target]
	if t == nil {
		return
	}
	obj, _ := h.params["targetInfo"].(map[string]any)
	if obj == nil {
		return
	}
	obj["url"] = t.url
	if t.parentFrame != "" {
		obj["parentFrameId"] = t.parentFrame
	}
}

func (g *guard) answerHistoryHoldReleaseLocked(h *heldSession, reply []byte, replyID int64, replySid string, hasReply, refuse bool) [][]byte {
	if !hasReply {
		return nil
	}
	if refuse {
		g.answerHeldLocked(refusal(replyID, replySid, refusedTarget))
		return g.takeAnsweredHeldLocked()
	}
	if len(reply) == 0 {
		return nil
	}
	if attachPassRefuseCut() && (g.hidden[h.id] || g.refused[h.id]) {
		g.answerHeldLocked(refusal(replyID, replySid, refusedTarget))
	} else {
		g.answerHeldLocked(reply)
	}
	return g.takeAnsweredHeldLocked()
}

func (g *guard) forwardHistoryHoldLocked(h *heldSession, attachSeq, readSeq int64, reply []byte, replyID int64, replySid string, hasReply bool) browserVerdict {
	if attachSeq > 0 && readSeq > 0 && readSeq < attachSeq && historyRestoreStaleDeliveryHides() {
		g.logf("cdp: refusing history hold delivery by stale confirm kind=%s session=%s attachSeq=%d readSeq=%d secondReads=%d", h.typ, h.id, attachSeq, readSeq, g.confirmSecondReads)
		return g.hideHistoryHoldLocked(h, attachSeq, readSeq, reply, replyID, replySid, hasReply)
	}
	g.ensureHistoryHoldParamsLocked(h)
	g.logf("cdp: history hold delivered kind=%s session=%s attachSeq=%d readSeq=%d readSeqs=%v secondReads=%d deadline=%t", h.typ, h.id, attachSeq, readSeq, h.historyReadSeqs, historyHoldSecondReads(h), h.historyDeadline)
	part := g.deliverHeldLocked(h)
	part.page = append(part.page, g.answerHistoryHoldReleaseLocked(h, reply, replyID, replySid, hasReply, false)...)
	return part
}

func (g *guard) hideHistoryHoldLocked(h *heldSession, attachSeq, readSeq int64, reply []byte, replyID int64, replySid string, hasReply bool) browserVerdict {
	g.logf("cdp: history hold hidden kind=%s session=%s attachSeq=%d readSeq=%d readSeqs=%v secondReads=%d deadline=%t", h.typ, h.id, attachSeq, readSeq, h.historyReadSeqs, historyHoldSecondReads(h), h.historyDeadline)
	part := g.hideHeldPageAndTabsLocked(h, true, "history-locked parent")
	part.page = append(part.page, g.answerHistoryHoldReleaseLocked(h, reply, replyID, replySid, hasReply, true)...)
	return part
}

func (g *guard) deliverHistoryHoldLocked(h *heldSession, allow bool, readSeq int64) browserVerdict {
	if h == nil {
		return browserVerdict{}
	}
	attachSeq := h.historyAttachMinSeq
	if allow && !g.historyHoldFreshEnoughLocked(h, readSeq) {
		g.logf("cdp: refusing history hold delivery by stale confirm kind=%s session=%s attachSeq=%d readSeq=%d", h.typ, h.id, attachSeq, readSeq)
		allow = false
	}
	delete(g.held, h.id)
	parent, snap := h.historyParent, h.historySnap
	reply, replyID, replySid, hasReply := h.reply, h.replyID, h.replySid, h.hasReply
	if allow && (h.historyUnresolved || g.historyHoldIdentityOKLocked(parent, snap)) {
		return g.forwardHistoryHoldLocked(h, attachSeq, readSeq, reply, replyID, replySid, hasReply)
	}
	return g.hideHistoryHoldLocked(h, attachSeq, readSeq, reply, replyID, replySid, hasReply)
}

func (g *guard) historyHoldAcceptsConfirmLocked(h *heldSession, issuedSeq int64) bool {
	if h == nil {
		return false
	}
	if issuedSeq <= 0 || !historyRestoreConfirmFreshness() || !historyRestoreRejectStaleInFlight() {
		return true
	}
	return h.historyConfirmMinSeq > 0 && issuedSeq >= h.historyConfirmMinSeq
}

func (g *guard) historyHoldNeedsConfirmReissueLocked(h *heldSession, priorIssued int64) bool {
	if h == nil {
		return false
	}
	if !historyRestoreReissueOnlyIfAcceptable() {
		return !g.historyHoldAcceptsConfirmLocked(h, priorIssued)
	}
	return h.historyConfirmMinSeq > 0 && h.historyConfirmMinSeq > priorIssued
}

func (g *guard) reissueHistoryConfirmIfNeededLocked(nt *navTarget, priorIssued int64) [][]byte {
	if nt == nil || !historyRestoreRejectStaleInFlight() {
		return nil
	}
	need := false
	for _, h := range g.held {
		if h == nil || h.historyParent != nt.target || h.historyUnresolved {
			continue
		}
		if g.historyHoldNeedsConfirmReissueLocked(h, priorIssued) {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	for _, h := range g.held {
		if h == nil || h.historyParent != nt.target || h.historyUnresolved {
			continue
		}
		if !g.historyHoldNeedsConfirmReissueLocked(h, priorIssued) {
			continue
		}
		n := len(h.historyReadSeqs)
		if n == 0 || h.historyReadSeqs[n-1] != priorIssued {
			h.historyReadSeqs = append(h.historyReadSeqs, priorIssued)
		}
	}
	g.confirmSecondReads++
	g.logf("cdp: history confirm reissued after stale in-flight read target=%s priorIssued=%d secondReads=%d", nt.target, priorIssued, g.confirmSecondReads)
	return g.beginHistoryParentConfirmLocked(nt)
}

func (g *guard) releaseHistoryHoldsForConfirmLocked(parent string, deliver bool, issuedSeq int64) [][]byte {
	if parent == "" {
		return nil
	}
	var matched []*heldSession
	for _, h := range g.held {
		if h == nil || h.historyParent != parent || h.historyUnresolved {
			continue
		}
		if issuedSeq > 0 && !g.historyHoldAcceptsConfirmLocked(h, issuedSeq) {
			continue
		}
		matched = append(matched, h)
	}
	var send [][]byte
	for _, h := range matched {
		if g.held[h.id] == nil {
			continue
		}
		part := g.deliverHistoryHoldLocked(h, deliver, issuedSeq)
		send = append(send, part.send...)
		g.pendingPage = append(g.pendingPage, part.page...)
	}
	if issuedSeq > 0 {
		send = append(send, g.reissueHistoryConfirmIfNeededLocked(g.navTargets[parent], issuedSeq)...)
	}
	return send
}

func (g *guard) hideHistoryHoldsForParentLocked(parent string) [][]byte {
	return g.releaseHistoryHoldsForConfirmLocked(parent, false, 0)
}

func (g *guard) redecideHistoryChildrenLocked(page string) [][]byte {
	if page == "" {
		return nil
	}
	var send [][]byte
	for _, t := range g.targets {
		if t == nil || t.gone || t.id == page || !historyRestoreChildKind(t.typ) {
			continue
		}
		owner := ""
		if t.typ == "iframe" {
			if p := g.iframePageLocked(t); p != nil {
				owner = p.id
			}
		} else {
			for _, id := range []string{t.opener, t.openerFrame, t.parentFrame} {
				if id == page {
					owner = page
					break
				}
			}
		}
		if owner != page {
			continue
		}
		prev := t.protected
		t.protected = true
		if !prev {
			send = append(send, g.protectLocked(t)...)
		}
	}
	return send
}

func (g *guard) noteHistoryLockedCommitLocked(nt *navTarget, before int) [][]byte {
	if nt == nil || nt.commits == before {
		return nil
	}
	if !g.o.Loaded(nt.documentURL) && !g.o.Loaded(nt.errorURL) {
		return nil
	}
	wasHistoryLocked := nt.historyLocked
	nt.historyLocked = true
	if !g.o.Loaded(nt.documentURL) {
		return nil
	}
	hasChildren := g.pageHasHistoryChildrenLocked(nt.target)
	if hasChildren && historyRestoreFirstProtectedWithChildren() {
		return g.navProtectedCommitLocked(nt)
	}
	if !wasHistoryLocked {
		return nil
	}
	return g.navProtectedCommitLocked(nt)
}

func (g *guard) navProtectedCommitLocked(nt *navTarget) [][]byte {
	if nt == nil || !historyRestoreConfirmAtNavCommit() {
		return nil
	}
	if !g.o.Loaded(nt.documentURL) {
		return nil
	}
	var send [][]byte
	if historyRestoreDetachOldChildren() {
		if t := g.targets[nt.target]; t != nil {
			send = append(send, g.markLocked(t)...)
			send = append(send, g.redecideHistoryChildrenLocked(t.id)...)
		}
	}
	g.beginConfirmLocked(nt)
	send = append(send, g.confirmTargetLocked(nt)...)
	send = append(send, g.hideHistoryHoldsForParentLocked(nt.target)...)
	return send
}
