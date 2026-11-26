package cdp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	maxDocIdentityHolds     = 256
	maxDocIdentityEmptyAsks = 3
	refusedDocRetriable     = "Execution context was destroyed."
)

type docIdentity struct {
	frameID  string
	loaderID string
	url      string
	full     bool
}

type docIdentityAdmit struct {
	taint uint64
}

type docIdentityHold struct {
	taint           uint64
	seq             uint64
	data            []byte
	id              int64
	session         string
	target          string
	method          string
	extensionAfter  bool
	extensionFrames []string
	arrived         time.Time
	deadline        time.Time
	timer           *time.Timer
	reservation     *link.Reservation
}

type docIdentityAsk struct {
	target     string
	started    time.Time
	infoDone   bool
	infoID     docIdentity
	askingInfo bool
	emptyTries int
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

func fetchURLPatternMatch(pattern, url string) bool {
	var rec func(pi, si int) bool
	rec = func(pi, si int) bool {
		for pi < len(pattern) || si < len(url) {
			if pi < len(pattern) && pattern[pi] == '*' {
				for k := si; k <= len(url); k++ {
					if rec(pi+1, k) {
						return true
					}
				}
				return false
			}
			if pi >= len(pattern) || si >= len(url) || pattern[pi] != url[si] {
				return false
			}
			pi++
			si++
		}
		return true
	}
	return rec(0, 0)
}

func (g *guard) docIdentityFetchGateCoversLocked(url string) bool {
	if g.o == nil || url == "" {
		return false
	}
	can, ok := protect.Canonical(url)
	if !ok || can == "" {
		return false
	}
	for _, p := range g.o.DocumentPatterns() {
		if fetchURLPatternMatch(p, can) {
			return true
		}
	}
	return false
}

func (g *guard) docIdentityNavFetchArmedLocked(target string) bool {
	nt := g.navTargets[target]
	return nt != nil && nt.session != "" && !nt.failed && nt.fetchArmed
}

func (g *guard) docIdentityStartedNetworkAllowLocked(target, url string) bool {
	if target == "" || url == "" || g.o == nil || chromeErrorDocument(url) {
		return false
	}
	if !g.docIdentityNavFetchArmedLocked(target) {
		return false
	}
	s := protect.Scheme(url)
	if s != "http" && s != "https" {
		return false
	}
	if protect.Unparseable(url) {
		return false
	}
	if !g.o.URL(url) {
		return false
	}
	return g.docIdentityFetchGateCoversLocked(url)
}

func (g *guard) noteDocIdentityStartedNavigatingLocked(target, url string) {
	if target == "" {
		return
	}
	if g.docIdentityStartedNetworkAllowLocked(target, url) {
		return
	}
	g.noteDocIdentityWatchURLLocked(target, url)
}

func (g *guard) noteDocIdentityPlaceLocked(target, url string) {
	if target == "" {
		return
	}
	if g.docPlaceURL == nil {
		g.docPlaceURL = map[string]string{}
	}
	if g.docPlaceSeq == nil {
		g.docPlaceSeq = map[string]uint64{}
	}
	if g.docPlaceSeq[target] > 0 && g.docPlaceURL[target] == url {
		return
	}
	g.docPlaceURL[target] = url
	g.docPlaceSeq[target]++
}

func (g *guard) recordDocIdentityChromeErrorNavLocked(nt *navTarget) {
	if nt == nil || !chromeErrorDocument(nt.documentURL) {
		return
	}
	if g.docChromeErrorSeq == nil {
		g.docChromeErrorSeq = map[string]uint64{}
	}
	g.docChromeErrorSeq[nt.target] = g.docIdentitySeq
	if g.docChromeErrorPlace == nil {
		g.docChromeErrorPlace = map[string]uint64{}
	}
	g.docChromeErrorPlace[nt.target] = g.docPlaceSeq[nt.target]
	if g.docChromeErrorFailed == nil {
		g.docChromeErrorFailed = map[string]string{}
	}
	g.docChromeErrorFailed[nt.target] = nt.errorURL
}

func (g *guard) docIdentityLiveChromeErrorNavLocked(target string) bool {
	nt := g.navTargets[target]
	if nt == nil || !chromeErrorDocument(nt.documentURL) || nt.errorURL == "" {
		return false
	}
	place, saw := g.docChromeErrorPlace[target]
	if !saw || place != g.docPlaceSeq[target] {
		return false
	}
	if g.docChromeErrorFailed[target] != nt.errorURL {
		return false
	}
	return true
}

func (g *guard) docIdentityLiveChromeErrorMatchLocked(target, infoURL string) bool {
	if !g.docIdentityLiveChromeErrorNavLocked(target) {
		return false
	}
	nt := g.navTargets[target]
	return infoURL != "" && infoURL == nt.errorURL
}

func (g *guard) noteOrdinaryDocIdentityFromTargetLocked(t *target) {
	if t == nil || (t.typ != "page" && t.typ != "tab") {
		return
	}
	g.noteDocIdentityWatchURLLocked(t.id, t.url)
	if g.docIdentityTargetURLIsLiveChromeErrorDestLocked(t.id, t.url) {
		return
	}
	g.noteDocIdentityPlaceLocked(t.id, t.url)
}

func (g *guard) docIdentityTargetURLIsLiveChromeErrorDestLocked(target, url string) bool {
	nt := g.navTargets[target]
	if nt == nil || url == "" || !chromeErrorDocument(nt.documentURL) || nt.errorURL == "" {
		return false
	}
	if url != nt.errorURL {
		return false
	}
	place, saw := g.docChromeErrorPlace[target]
	if !saw || place != g.docPlaceSeq[target] {
		return false
	}
	if g.docChromeErrorFailed[target] != nt.errorURL {
		return false
	}
	return true
}

func (g *guard) noteOrdinaryDocIdentityFromNavLocked(nt *navTarget) {
	if nt == nil {
		return
	}
	g.noteDocIdentityWatchURLLocked(nt.target, nt.documentURL)
	g.noteDocIdentityPlaceLocked(nt.target, nt.documentURL)
	g.recordDocIdentityChromeErrorNavLocked(nt)
}

func (g *guard) docIdentityPinnedNeedsHoldLocked(c *command) bool {
	if c == nil || !docIdentityPinned(c) {
		return false
	}
	t := g.sessionTargetLocked(c.session)
	if t == nil || !navTypes[t.typ] {
		return false
	}
	place := g.docPlaceSeq[t.id]
	ctxPlace, ok := g.shownContextPlaceSeqLocked(t.id, c.params)
	if !ok {
		return false
	}
	if ctxPlace < place {
		return true
	}
	cur := g.currentDocIdentityLocked(t.id)
	if cur.url == "" {
		cur.url = t.url
	}
	return g.docIdentityWatchBadLocked(cur.url) || g.docIdentityWatchBadLocked(t.url)
}

func (g *guard) shownContextPlaceSeqLocked(target string, params map[string]any) (uint64, bool) {
	s := g.shown[target]
	if s == nil {
		return 0, false
	}
	if id, ok := intParam(params, "contextId"); ok {
		if sc := s.contexts[id]; sc != nil {
			return sc.placeSeq, true
		}
	}
	if id, ok := intParam(params, "executionContextId"); ok {
		if sc := s.contexts[id]; sc != nil {
			return sc.placeSeq, true
		}
	}
	if u, ok := params["uniqueContextId"].(string); ok && u != "" {
		for _, sc := range s.contexts {
			if sc != nil && sc.unique == u {
				return sc.placeSeq, true
			}
		}
	}
	if u, ok := params["executionContextUniqueId"].(string); ok && u != "" {
		for _, sc := range s.contexts {
			if sc != nil && sc.unique == u {
				return sc.placeSeq, true
			}
		}
	}
	return 0, false
}

func intParam(params map[string]any, key string) (int64, bool) {
	if params == nil {
		return 0, false
	}
	switch v := params[key].(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func (g *guard) noteDocIdentityAdmitLocked(c *command) {
	if c == nil || !docIdentityCoveredMethod(c.method) {
		return
	}
	if docIdentityPinned(c) && !g.docIdentityPinnedNeedsHoldLocked(c) {
		return
	}
	t := g.sessionTargetLocked(c.session)
	if t == nil || !navTypes[t.typ] || g.docIdentitySkipTargetLocked(t.id) {
		return
	}
	admit := docIdentityAdmit{taint: g.docIdentityTaint[t.id]}
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
		return nil, [][]byte{retriableContextDestroyed(*m.ID, m.SessionID)}, true
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
	if ask := g.docIdentityAsks[target]; ask != nil && ask.askingInfo {
		return nil
	}
	if g.docIdentityAsks == nil {
		g.docIdentityAsks = map[string]*docIdentityAsk{}
	}
	started := after
	if started.IsZero() {
		started = time.Now()
	}
	ask := &docIdentityAsk{target: target, started: started}
	g.docIdentityAsks[target] = ask
	if g.relaySession == "" {
		return g.failDocIdentityHoldsLocked(target, true)
	}
	ask.askingInfo = true
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
		return g.answeredDocIdentityInfoLocked(target, m)
	})}
}

func (g *guard) docIdentityHoldsWithinDeadlineLocked(target string) bool {
	now := time.Now()
	for _, h := range g.docIdentityHolds {
		if h != nil && h.target == target && now.Before(h.deadline) {
			return true
		}
	}
	return false
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
	if result.TargetInfo.URL == "" {
		ask.emptyTries++
		if ask.emptyTries < maxDocIdentityEmptyAsks && g.docIdentityHoldsWithinDeadlineLocked(target) {
			ask.infoDone = false
			ask.infoID = docIdentity{}
			ask.askingInfo = true
			return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": target}, func(m message) [][]byte {
				return g.answeredDocIdentityInfoLocked(target, m)
			})}
		}
		delete(g.docIdentityAsks, target)
		return g.failDocIdentityHoldsLocked(target, false)
	}
	ask.infoID = docIdentity{frameID: target, url: result.TargetInfo.URL, full: true}
	return g.settleDocIdentityAskLocked(target)
}

func (g *guard) settleDocIdentityAskLocked(target string) [][]byte {
	ask := g.docIdentityAsks[target]
	if ask == nil {
		return nil
	}
	if ask.askingInfo {
		return nil
	}
	if !ask.infoDone {
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
	if id.url == "" || g.docIdentityProtectedLocked(id) || chromeErrorDocument(id.url) {
		return false
	}
	if protect.Inheriting(id.url) {
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
	kPaint := callKey{h.session, h.id}
	if h.reservation != nil {
		if h.extensionAfter {
			if p := g.heldExtensionPaints[kPaint]; p != nil && p.reservation == nil {
				p.reservation = h.reservation
				h.reservation = nil
			}
		}
		if h.reservation != nil {
			g.pendingRelease = append(g.pendingRelease, h.reservation)
			h.reservation = nil
		}
	}
	var send [][]byte
	tainted := g.docIdentityTaint[h.target] != h.taint
	switch {
	case g.sessionRefusedLocked(h.session):
		g.refuseDocIdentityHoldPageLocked(h)
	case chromeErrorDocument(current.url):
		eventSeq, saw := g.docChromeErrorSeq[h.target]
		if saw && h.seq < eventSeq {
			send = append(send, g.deliverDocIdentityDataLocked(h)...)
		} else {
			g.dropDeferredExtensionPaintLocked(kPaint)
			g.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))
		}
	case g.docIdentityLiveChromeErrorMatchLocked(h.target, current.url) && g.docIdentityProtectedLocked(current):
		g.dropDeferredExtensionPaintLocked(kPaint)
		g.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))
	case g.docIdentityProtectedLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.refuseDocIdentityHoldPageLocked(h)
	case !g.docIdentityOrdinaryLocked(current):
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.refuseDocIdentityHoldPageLocked(h)
	case tainted:
		send = append(send, g.cutDocIdentitySessionLocked(h.session, h.target)...)
		g.refuseDocIdentityHoldPageLocked(h)
	default:
		send = append(send, g.deliverDocIdentityDataLocked(h)...)
	}
	return send
}

func (g *guard) deliverDocIdentityDataLocked(h *docIdentityHold) [][]byte {
	if h == nil {
		return nil
	}
	if h.extensionAfter && protect.ExtensionShownMethod(h.method) && g.extensionFramesPresentLocked(h.target) {
		return g.startDeferredExtensionPaintProbeLocked(callKey{h.session, h.id})
	}
	g.dropDeferredExtensionPaintLocked(callKey{h.session, h.id})
	g.pendingPage = append(g.pendingPage, h.data)
	return nil
}

func (g *guard) refuseDocIdentityHoldPageLocked(h *docIdentityHold) {
	if h == nil {
		return
	}
	g.dropDeferredExtensionPaintLocked(callKey{h.session, h.id})
	g.pendingPage = append(g.pendingPage, refusal(h.id, h.session, refusedTarget))
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
		cur := g.currentDocIdentityLocked(target)
		ordinary := g.docIdentityOrdinaryLocked(cur) && !g.docIdentityProtectedLocked(cur)
		if g.docIdentityLiveChromeErrorNavLocked(target) {
			g.pendingPage = append(g.pendingPage, retriableContextDestroyed(h.id, h.session))
		} else if cut && !ordinary {
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
	liveChromeError := g.docIdentityLiveChromeErrorNavLocked(target)
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	g.releaseDocIdentityReservationLocked(h)
	delete(g.docIdentityHolds, k)
	var send, page [][]byte
	if liveChromeError || ordinary {
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
		g.answerPrunedDocIdentityLocked(h)
	}
	for k := range g.docIdentityCalls {
		if k.session == session {
			delete(g.docIdentityCalls, k)
		}
	}
}

func (g *guard) dropDocIdentityHoldLocked(k callKey) {
	h := g.docIdentityHolds[k]
	if h == nil {
		return
	}
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	target := h.target
	g.releaseDocIdentityReservationLocked(h)
	delete(g.docIdentityHolds, k)
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
}

func (g *guard) pruneDocIdentityTargetLocked(target string) {
	if target == "" {
		return
	}
	delete(g.docIdentityTaint, target)
	delete(g.docChromeErrorSeq, target)
	delete(g.docChromeErrorPlace, target)
	delete(g.docChromeErrorFailed, target)
	delete(g.docPlaceSeq, target)
	delete(g.docPlaceURL, target)
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
		g.answerPrunedDocIdentityLocked(h)
	}
}

func (g *guard) answerPrunedDocIdentityLocked(h *docIdentityHold) {
	if h == nil {
		return
	}
	k := callKey{h.session, h.id}
	if !heldReplyAnswerDocIdentity() {
		return
	}
	g.dropDeferredExtensionPaintLocked(k)
	if g.sessionRefusedLocked(h.session) || g.targetProtectedLocked(h.target) {
		g.answerHeldLocked(refusal(h.id, h.session, refusedTarget))
		return
	}
	g.answerHeldLocked(retriableContextDestroyed(h.id, h.session))
}
