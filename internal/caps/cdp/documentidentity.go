package cdp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	maxDocIdentityHolds = 256
	refusedDocRetriable = "Execution context was destroyed."
)

type docIdentity struct {
	frameID  string
	loaderID string
	url      string
}

func (a docIdentity) equal(b docIdentity) bool {
	return a.frameID == b.frameID && a.loaderID == b.loaderID && a.url == b.url
}

type docIdentityHold struct {
	baseline docIdentity
	data     []byte
	id       int64
	session  string
	target   string
	arrived  time.Time
	deadline time.Time
	timer    *time.Timer
}

type docIdentityAsk struct {
	target     string
	started    time.Time
	infoDone   bool
	treeDone   bool
	infoID     docIdentity
	askingInfo bool
	askingTree bool
}

func docIdentityCoveredMethod(method string) bool {
	switch method {
	case "Runtime.evaluate",
		"DOM.getDocument", "DOM.getOuterHTML",
		"DOMSnapshot.captureSnapshot",
		"Page.captureScreenshot", "Page.printToPDF",
		"Page.getResourceContent", "Page.getResourceTree",
		"Accessibility.getFullAXTree", "Accessibility.getPartialAXTree", "Accessibility.queryAXTree":
		return true
	default:
		return false
	}
}

func docIdentityPinned(c *command) bool {
	if c == nil || c.params == nil {
		return false
	}
	for _, key := range []string{"contextId", "uniqueContextId", "executionContextId", "executionContextUniqueId", "objectId", "promiseObjectId"} {
		if _, ok := c.params[key]; ok {
			return true
		}
	}
	return false
}

func (g *guard) docIdentitySkipTargetLocked(target string) bool {
	if target == "" {
		return true
	}
	if g.errorProofOfLocked(target) != errorProofNone {
		return true
	}
	t := g.targets[target]
	if t != nil && chromeErrorDocument(t.url) {
		return true
	}
	nt := g.navTargets[target]
	if nt != nil && (chromeErrorDocument(nt.documentURL) || nt.errorURL != "" && g.o.Loaded(nt.errorURL)) {
		return true
	}
	return false
}

func (g *guard) currentDocIdentityLocked(target string) docIdentity {
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

func (g *guard) noteOrdinaryDocIdentityLocked(target, frameID, loaderID, url string) {
	if target == "" || g.docIdentitySkipTargetLocked(target) {
		return
	}
	if url != "" && (g.o.Loaded(url) || protect.Unparseable(url)) {
		return
	}
	if chromeErrorDocument(url) {
		return
	}
	if frameID == "" {
		frameID = target
	}
	if g.docBaselines == nil {
		g.docBaselines = map[string]docIdentity{}
	}
	g.docBaselines[target] = docIdentity{frameID: frameID, loaderID: loaderID, url: url}
}

func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {
	if t == nil || t.protected || (t.typ != "page" && t.typ != "tab") || t.url == "" || g.o.Loaded(t.url) || chromeErrorDocument(t.url) {
		return
	}
	loader := ""
	if nt := g.navTargets[t.id]; nt != nil {
		loader = nt.loaderID
	}
	g.noteOrdinaryDocIdentityLocked(t.id, t.id, loader, t.url)
}

func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {
	if nt == nil || nt.documentURL == "" || g.o.Loaded(nt.documentURL) || chromeErrorDocument(nt.documentURL) || nt.errorURL != "" {
		return
	}
	g.noteOrdinaryDocIdentityLocked(nt.target, nt.target, nt.loaderID, nt.documentURL)
}

func (g *guard) noteDocIdentityAdmitLocked(c *command) {
	if c == nil || !docIdentityCoveredMethod(c.method) || docIdentityPinned(c) {
		return
	}
	t := g.sessionTargetLocked(c.session)
	if t == nil || !navTypes[t.typ] || g.docIdentitySkipTargetLocked(t.id) {
		return
	}
	baseline, ok := g.docBaselines[t.id]
	if !ok {
		baseline = g.currentDocIdentityLocked(t.id)
		if baseline.url == "" {
			baseline.url = t.url
		}
		if baseline.frameID == "" {
			baseline.frameID = t.id
		}
		g.noteOrdinaryDocIdentityLocked(t.id, baseline.frameID, baseline.loaderID, baseline.url)
		baseline = g.docBaselines[t.id]
		if baseline.frameID == "" {
			baseline = g.currentDocIdentityLocked(t.id)
		}
	}
	k := callKey{c.session, c.id}
	if g.docIdentityCalls == nil {
		g.docIdentityCalls = map[callKey]docIdentity{}
	}
	g.docIdentityCalls[k] = baseline
}

func (g *guard) dropDocIdentityCallLocked(k callKey) {
	delete(g.docIdentityCalls, k)
}

func (g *guard) sessionDocIdentityHoldCountLocked(session string) int {
	n := 0
	for _, h := range g.docIdentityHolds {
		if h != nil && h.session == session {
			n++
		}
	}
	return n
}

func retriableContextDestroyed(id int64, session string) []byte {
	m := map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": refusedDocRetriable}}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (g *guard) holdDocIdentityReplyLocked(k callKey, m *message, data []byte) (send, page [][]byte, held bool) {
	baseline, ok := g.docIdentityCalls[k]
	delete(g.docIdentityCalls, k)
	if !ok || m == nil || m.ID == nil {
		return nil, nil, false
	}
	target := g.worldTargetLocked(m.SessionID)
	if target == "" || g.docIdentitySkipTargetLocked(target) {
		return nil, nil, false
	}
	if g.sessionDocIdentityHoldCountLocked(m.SessionID) >= maxDocIdentityHolds {
		g.logf("cdp: too many document identity holds on a session; dropping one")
		return nil, [][]byte{retriableContextDestroyed(*m.ID, m.SessionID)}, true
	}
	now := time.Now()
	h := &docIdentityHold{
		baseline: baseline,
		data:     append([]byte(nil), data...),
		id:       *m.ID,
		session:  m.SessionID,
		target:   target,
		arrived:  now,
		deadline: now.Add(holdTimeout()),
	}
	if g.docIdentityHolds == nil {
		g.docIdentityHolds = map[callKey]*docIdentityHold{}
	}
	g.docIdentityHolds[k] = h
	h.timer = time.AfterFunc(holdTimeout(), func() {
		g.expireDocIdentityHold(k)
	})
	return g.ensureDocIdentityAskLocked(target, now), nil, true
}

func (g *guard) ensureDocIdentityAskLocked(target string, after time.Time) [][]byte {
	if target == "" {
		return nil
	}
	if ask := g.docIdentityAsks[target]; ask != nil && (ask.askingInfo || ask.askingTree) {
		_ = after
		return nil
	}
	if g.docIdentityAsks == nil {
		g.docIdentityAsks = map[string]*docIdentityAsk{}
	}
	ask := &docIdentityAsk{target: target, started: time.Now()}
	g.docIdentityAsks[target] = ask
	var send [][]byte
	if nt := g.navTargets[target]; nt != nil && nt.session != "" {
		ask.askingTree = true
		ask.infoDone = true
		send = append(send, g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(m message) [][]byte {
			return g.answeredDocIdentityTreeLocked(target, m)
		}))
		return send
	}
	if g.relaySession == "" {
		return g.failDocIdentityHoldsLocked(target, true)
	}
	ask.askingInfo = true
	ask.treeDone = true
	send = append(send, g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
		return g.answeredDocIdentityInfoLocked(target, m)
	}))
	return send
}

func (g *guard) answeredDocIdentityInfoLocked(target string, m message) [][]byte {
	ask := g.docIdentityAsks[target]
	if ask == nil {
		return nil
	}
	ask.askingInfo = false
	ask.infoDone = true
	var result struct {
		TargetInfo targetInfo `json:"targetInfo"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.TargetInfo.TargetID == "" || result.TargetInfo.TargetID != target {
		ask.infoID = docIdentity{}
		return g.settleDocIdentityAskLocked(target)
	}
	id := docIdentity{frameID: target, url: result.TargetInfo.URL}
	if nt := g.navTargets[target]; nt != nil && nt.loaderID != "" {
		id.loaderID = nt.loaderID
	}
	ask.infoID = id
	if result.TargetInfo.URL != "" && !g.o.Loaded(result.TargetInfo.URL) && !chromeErrorDocument(result.TargetInfo.URL) {
		g.noteOrdinaryDocIdentityLocked(target, target, id.loaderID, result.TargetInfo.URL)
	}
	if g.docIdentityProtectedLocked(id) || !ask.askingTree || g.docIdentityInfoSufficientLocked(target, ask, id) {
		ask.askingTree = false
		ask.treeDone = true
	}
	return g.settleDocIdentityAskLocked(target)
}

func (g *guard) docIdentityInfoSufficientLocked(target string, ask *docIdentityAsk, id docIdentity) bool {
	for _, h := range g.docIdentityHolds {
		if h == nil || h.target != target || h.arrived.After(ask.started) {
			continue
		}
		if h.baseline.equal(id) {
			return false
		}
	}
	return true
}

func (g *guard) answeredDocIdentityTreeLocked(target string, m message) [][]byte {
	ask := g.docIdentityAsks[target]
	if ask == nil {
		return nil
	}
	ask.askingTree = false
	ask.treeDone = true
	var result struct {
		FrameTree *struct {
			Frame struct {
				ID             string `json:"id"`
				ParentID       string `json:"parentId"`
				LoaderID       string `json:"loaderId"`
				URL            string `json:"url"`
				SecurityOrigin string `json:"securityOrigin"`
				UnreachableURL string `json:"unreachableUrl"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || result.FrameTree == nil || result.FrameTree.Frame.ID != target || result.FrameTree.Frame.ParentID != "" {
		return g.settleDocIdentityAskLocked(target)
	}
	frame := result.FrameTree.Frame
	if nt := g.navTargets[target]; nt != nil {
		nt.documentURL = frame.URL
		nt.loaderID = frame.LoaderID
		nt.errorURL = frame.UnreachableURL
		if frame.SecurityOrigin != "" {
			nt.securityOrigin = frame.SecurityOrigin
		}
	}
	url := frame.URL
	if frame.UnreachableURL != "" {
		url = frame.UnreachableURL
	}
	ask.infoID = docIdentity{frameID: frame.ID, loaderID: frame.LoaderID, url: url}
	if url != "" && !g.o.Loaded(url) && !chromeErrorDocument(url) && frame.UnreachableURL == "" {
		g.noteOrdinaryDocIdentityLocked(target, frame.ID, frame.LoaderID, frame.URL)
	}
	return g.settleDocIdentityAskLocked(target)
}

func (g *guard) settleDocIdentityAskLocked(target string) [][]byte {
	ask := g.docIdentityAsks[target]
	if ask == nil {
		return nil
	}
	if ask.askingInfo || ask.askingTree {
		if ask.infoDone && g.docIdentityProtectedLocked(ask.infoID) {
			ask.askingTree = false
			ask.treeDone = true
		} else {
			return nil
		}
	}
	if !ask.infoDone && !ask.treeDone {
		delete(g.docIdentityAsks, target)
		return g.failDocIdentityHoldsLocked(target, true)
	}
	id := ask.infoID
	started := ask.started
	delete(g.docIdentityAsks, target)
	if id.frameID == "" && id.loaderID == "" && id.url == "" {
		return g.failDocIdentityHoldsLocked(target, true)
	}
	var send [][]byte
	var again []callKey
	for k, h := range g.docIdentityHolds {
		if h == nil || h.target != target {
			continue
		}
		if h.arrived.After(started) {
			again = append(again, k)
			continue
		}
		send = append(send, g.releaseDocIdentityHoldLocked(k, h, id)...)
	}
	if len(again) > 0 {
		var earliest time.Time
		for i, k := range again {
			h := g.docIdentityHolds[k]
			if h == nil {
				continue
			}
			if i == 0 || h.arrived.Before(earliest) {
				earliest = h.arrived
			}
		}
		send = append(send, g.ensureDocIdentityAskLocked(target, earliest)...)
	}
	return send
}

func (g *guard) docIdentityProtectedLocked(id docIdentity) bool {
	if id.url != "" && (g.o.Loaded(id.url) || protect.Unparseable(id.url)) {
		return true
	}
	return false
}

func (g *guard) docIdentityOrdinaryLocked(id docIdentity) bool {
	if g.docIdentityProtectedLocked(id) || chromeErrorDocument(id.url) {
		return false
	}
	if id.url == "" || protect.Inheriting(id.url) {
		return true
	}
	if strings.HasPrefix(strings.ToLower(id.url), "http://") || strings.HasPrefix(strings.ToLower(id.url), "https://") {
		return !g.o.Loaded(id.url)
	}
	return !g.o.Loaded(id.url)
}

func (g *guard) releaseDocIdentityHoldLocked(k callKey, h *docIdentityHold, current docIdentity) [][]byte {
	if h == nil {
		return nil
	}
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	delete(g.docIdentityHolds, k)
	var send [][]byte
	switch {
	case g.docIdentityProtectedLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
	case !g.docIdentityOrdinaryLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
	case !current.equal(h.baseline):
		g.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))
	default:
		g.pendingPage = append(g.pendingPage, h.data)
	}
	return send
}

func (g *guard) failDocIdentityHoldsLocked(target string, cut bool) [][]byte {
	var send [][]byte
	for k, h := range g.docIdentityHolds {
		if h == nil || h.target != target {
			continue
		}
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
		delete(g.docIdentityHolds, k)
		ordinary := false
		cur := g.currentDocIdentityLocked(target)
		ordinary = g.docIdentityOrdinaryLocked(cur) && !g.docIdentityProtectedLocked(cur)
		if cut && !ordinary {
			send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
			g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
		} else {
			g.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))
		}
	}
	delete(g.docIdentityAsks, target)
	return send
}

func (g *guard) cutDocIdentitySessionLocked(session, target string) [][]byte {
	if session == "" {
		return nil
	}
	s := g.sessions[session]
	if s == nil {
		g.addRefusedLocked(session)
		return nil
	}
	if s.refused {
		return nil
	}
	s.refused = true
	g.addRefusedLocked(session)
	var out [][]byte
	if s.waiting {
		out = append(out, g.resumeLocked(session, target))
	}
	parent := s.parent
	if parent == "" {
		parent = g.attachParent
	}
	msg, _ := g.newCallLocked(parent, "Target.detachFromTarget", map[string]any{"sessionId": session}, false)
	return append(out, msg)
}

func (g *guard) expireDocIdentityHold(k callKey) {
	g.mu.Lock()
	h := g.docIdentityHolds[k]
	if h == nil {
		g.mu.Unlock()
		return
	}
	if rem := time.Until(h.deadline); rem > 0 {
		h.timer = time.AfterFunc(rem, func() { g.expireDocIdentityHold(k) })
		g.mu.Unlock()
		return
	}
	target := h.target
	cur := g.currentDocIdentityLocked(target)
	ordinary := g.docIdentityOrdinaryLocked(cur) && !g.docIdentityProtectedLocked(cur)
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	delete(g.docIdentityHolds, k)
	var send, page [][]byte
	if ordinary {
		page = append(page, retriableContextDestroyed(h.id, h.session))
	} else {
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		page = append(page, refusal(h.id, h.session, refusedTarget))
	}
	still := false
	for _, o := range g.docIdentityHolds {
		if o != nil && o.target == target {
			still = true
			break
		}
	}
	if !still {
		delete(g.docIdentityAsks, target)
	}
	flush := g.flushAsync
	g.mu.Unlock()
	if flush != nil {
		flush(send, page)
	}
}

func (g *guard) pruneDocIdentitySessionLocked(session string) {
	if session == "" {
		return
	}
	for k, h := range g.docIdentityHolds {
		if h == nil || h.session != session {
			continue
		}
		if h.timer != nil {
			h.timer.Stop()
		}
		delete(g.docIdentityHolds, k)
	}
	for k := range g.docIdentityCalls {
		if k.session == session {
			delete(g.docIdentityCalls, k)
		}
	}
}

func (g *guard) pruneDocIdentityTargetLocked(target string) {
	if target == "" {
		return
	}
	delete(g.docBaselines, target)
	delete(g.docIdentityAsks, target)
	for k, h := range g.docIdentityHolds {
		if h == nil || h.target != target {
			continue
		}
		if h.timer != nil {
			h.timer.Stop()
		}
		delete(g.docIdentityHolds, k)
	}
}
