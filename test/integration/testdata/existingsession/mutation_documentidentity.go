package cdp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
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
	full     bool
}

func (a docIdentity) equal(b docIdentity) bool {
	if a.full && b.full {
		return a.frameID == b.frameID && a.loaderID == b.loaderID && a.url == b.url
	}
	return a.url == b.url
}

type docIdentityAdmit struct {
	baseline docIdentity
	known    bool
	taint    uint64
}

type docIdentityHold struct {
	baseline    docIdentity
	known       bool
	taint       uint64
	seq         uint64
	data        []byte
	id          int64
	session     string
	target      string
	arrived     time.Time
	deadline    time.Time
	timer       *time.Timer
	reservation *link.Reservation
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
		"Page.getResourceContent",
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
	return g.errorProofOfLocked(target) != errorProofNone
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

func (g *guard) noteOrdinaryDocIdentityLocked(target, frameID, loaderID, url string, full bool) {
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
	g.docBaselines[target] = docIdentity{frameID: frameID, loaderID: loaderID, url: url, full: full}
}

func (g *guard) docIdentityWatchBadLocked(url string) bool {
	if url == "" || chromeErrorDocument(url) {
		return false
	}
	if protect.Unparseable(url) || g.o.Loaded(url) {
		return true
	}
	return false
}

func (g *guard) bumpDocIdentityTaintLocked(target string) {
	if target == "" {
		return
	}
	if g.docIdentityTaint == nil {
		g.docIdentityTaint = map[string]uint64{}
	}
	g.docIdentityTaint[target]++
}

func (g *guard) noteDocIdentityWatchURLLocked(target, url string) {
	if target == "" || !g.docIdentityWatchBadLocked(url) {
		return
	}
	g.bumpDocIdentityTaintLocked(target)
}

func (g *guard) noteDocIdentityChromeErrorCommitLocked(target, url string) {
	if target == "" || !chromeErrorDocument(url) {
		return
	}
	if g.docChromeErrorSeq == nil {
		g.docChromeErrorSeq = map[string]uint64{}
	}
	g.docChromeErrorSeq[target] = g.docIdentitySeq
}

func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {
	if t == nil || (t.typ != "page" && t.typ != "tab") {
		return
	}
	g.noteDocIdentityChromeErrorCommitLocked(t.id, t.url)
	g.noteDocIdentityWatchURLLocked(t.id, t.url)
}

func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {
	if nt == nil {
		return
	}
	g.noteDocIdentityChromeErrorCommitLocked(nt.target, nt.documentURL)
	g.noteDocIdentityWatchURLLocked(nt.target, nt.documentURL)
}

func (g *guard) noteDocIdentityAdmitLocked(c *command) {
	if c == nil || !docIdentityCoveredMethod(c.method) || docIdentityPinned(c) {
		return
	}
	t := g.sessionTargetLocked(c.session)
	if t == nil || !navTypes[t.typ] || g.docIdentitySkipTargetLocked(t.id) {
		return
	}
	admit := docIdentityAdmit{taint: g.docIdentityTaint[t.id]}
	if baseline, ok := g.docBaselines[t.id]; ok && baseline.full {
		admit.baseline = baseline
		admit.known = true
	} else {
		cur := g.currentDocIdentityLocked(t.id)
		if cur.url == "" {
			cur.url = t.url
		}
		admit.baseline = docIdentity{url: cur.url}
	}
	k := callKey{c.session, c.id}
	if g.docIdentityCalls == nil {
		g.docIdentityCalls = map[callKey]docIdentityAdmit{}
	}
	g.docIdentityCalls[k] = admit
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
	admit, ok := g.docIdentityCalls[k]
	delete(g.docIdentityCalls, k)
	if !ok || m == nil || m.ID == nil {
		return nil, nil, false
	}
	if g.relaySession == "" {
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
	reservation, reserved := g.holdReservation(data)
	if !reserved {
		g.logf("cdp: document identity hold exceeded the write budget; refusing the reply")
		return nil, [][]byte{retriableContextDestroyed(*m.ID, m.SessionID)}, true
	}
	now := time.Now()
	h := &docIdentityHold{
		baseline:    admit.baseline,
		known:       admit.known,
		taint:       admit.taint,
		seq:         g.docIdentitySeq,
		data:        append([]byte(nil), data...),
		id:          *m.ID,
		session:     m.SessionID,
		target:      target,
		arrived:     now,
		deadline:    now.Add(holdTimeout()),
		reservation: reservation,
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
	if g.relaySession == "" {
		return g.failDocIdentityHoldsLocked(target, true)
	}
	ask.askingInfo = true
	ask.treeDone = true
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
		return g.answeredDocIdentityInfoLocked(target, m)
	})}
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
	id := docIdentity{frameID: target, url: result.TargetInfo.URL, full: true}
	ask.infoID = id
	if result.TargetInfo.URL != "" && !g.o.Loaded(result.TargetInfo.URL) && !chromeErrorDocument(result.TargetInfo.URL) {
		g.noteOrdinaryDocIdentityLocked(target, target, "", result.TargetInfo.URL, true)
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
	if h.reservation != nil {
		g.pendingRelease = append(g.pendingRelease, h.reservation)
		h.reservation = nil
	}
	var send [][]byte
	tainted := g.docIdentityTaint[h.target] != h.taint
	switch {
	case chromeErrorDocument(current.url):
		g.pendingPage = append(g.pendingPage, h.data)
	case g.docIdentityProtectedLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
	case !g.docIdentityOrdinaryLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
	case tainted:
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
	default:
		if current.full {
			g.noteOrdinaryDocIdentityLocked(h.target, current.frameID, current.loaderID, current.url, true)
		}
		g.pendingPage = append(g.pendingPage, h.data)
	}
	return send
}

func (g *guard) releaseDocIdentityReservationLocked(h *docIdentityHold) {
	if h == nil || h.reservation == nil {
		return
	}
	g.pendingRelease = append(g.pendingRelease, h.reservation)
	h.reservation = nil
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
		g.releaseDocIdentityReservationLocked(h)
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
	g.releaseDocIdentityReservationLocked(h)
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
	releases := g.takeReleasesLocked()
	flush := g.flushAsync
	g.mu.Unlock()
	releaseReservations(releases)
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
		g.releaseDocIdentityReservationLocked(h)
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
	delete(g.docIdentityTaint, target)
	delete(g.docChromeErrorSeq, target)
	delete(g.docIdentityAsks, target)
	for k, h := range g.docIdentityHolds {
		if h == nil || h.target != target {
			continue
		}
		if h.timer != nil {
			h.timer.Stop()
		}
		g.releaseDocIdentityReservationLocked(h)
		delete(g.docIdentityHolds, k)
	}
}
