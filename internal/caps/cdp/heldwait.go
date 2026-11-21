package cdp

import (
	"time"
)

const (
	maxParkedPerIframe = 8
	maxParkedPerPage   = 32
	maxParkedPerStream = 256
)

var heldIframeMethods = map[string]bool{
	"Target.attachToTarget": true,
	"Target.getTargetInfo":  true,
}

type heldWait struct {
	target  string
	page    string
	ch      chan struct{}
	timer   *time.Timer
	calls   map[callKey]bool
	refused bool
	expired bool
}

func (g *guard) heldDeadlineLocked(id string) time.Time {
	var first time.Time
	for _, h := range g.held {
		if h != nil && h.target == id && (first.IsZero() || h.at.Before(first)) {
			first = h.at
		}
	}
	return first.Add(holdTimeout())
}

func (g *guard) heldPageLocked(id string) string {
	for _, h := range g.held {
		if h == nil || h.target != id {
			continue
		}
		if owner := g.sessionOwnerLocked(h.parent); owner != nil {
			return owner.id
		}
	}
	return ""
}

func (g *guard) parkedCountsLocked(page string) (perPage, perStream int) {
	for _, w := range g.heldWaits {
		perStream += len(w.calls)
		if page != "" && w.page == page {
			perPage += len(w.calls)
		}
	}
	return perPage, perStream
}

func (g *guard) heldIframeWaitLocked(c *command) <-chan struct{} {
	id := c.str("targetId")
	if !heldIframeMethods[c.method] || id == "" {
		return nil
	}
	k := callKey{c.session, c.id}
	if _, answered := g.parkedAnswers[k]; answered {
		return nil
	}
	if !g.heldIframeParkableLocked(id) {
		return nil
	}
	w := g.heldWaits[id]
	if w != nil && w.calls[k] {
		return w.ch
	}
	if w = g.heldWaitForLocked(id); w == nil {
		return nil
	}
	if g.heldWaitFullLocked(w) {
		g.parkedAnswers[k] = false
		g.logf("cdp: refused %s at once: too many calls wait for held iframes target=%s", c.method, id)
		return nil
	}
	w.calls[k] = true
	g.logf("cdp: holding %s until the guard decides an auto-attached iframe target=%s", c.method, id)
	return w.ch
}

func (g *guard) heldIframeParkableLocked(id string) bool {
	t := g.targets[id]
	return t != nil && !t.gone && t.typ == "iframe" && !t.protected && g.iframeHeldLocked(id)
}

func (g *guard) heldWaitForLocked(id string) *heldWait {
	if w := g.heldWaits[id]; w != nil {
		if w.expired {
			return nil
		}
		return w
	}
	deadline := g.heldDeadlineLocked(id)
	if !time.Now().Before(deadline) {
		return nil
	}
	w := &heldWait{target: id, page: g.heldPageLocked(id), ch: make(chan struct{}), calls: map[callKey]bool{}}
	g.heldWaits[id] = w
	if heldLookupDeadline() {
		w.timer = time.AfterFunc(time.Until(deadline), func() { g.expireHeldWait(w) })
	}
	return w
}

func (g *guard) heldWaitFullLocked(w *heldWait) bool {
	perPage, perStream := g.parkedCountsLocked(w.page)
	return len(w.calls) >= maxParkedPerIframe || (w.page != "" && perPage >= maxParkedPerPage) || perStream >= maxParkedPerStream
}

func (g *guard) hiddenResultLocked(kind callKind, m *message, res map[string]any, v browserVerdict) browserVerdict {
	if kind == kindTargetInfo && g.parkTargetInfoReplyLocked(callKey{m.SessionID, *m.ID}, m, res) {
		return v
	}
	v.forward = missingTarget(*m.ID, m.SessionID)
	return v
}

func (g *guard) parkTargetInfoReplyLocked(k callKey, m *message, res map[string]any) bool {
	if !heldLookupParkReply() || (k.session != "" && !g.browserSessions[k.session]) {
		return false
	}
	info, _ := toInfo(res["targetInfo"])
	id := info.TargetID
	v := g.infoVerdictLocked(info)
	if id == "" || info.Type != "iframe" || !g.heldIframeParkableLocked(id) || v.loaded() || v.extensionFrame || g.extensionFrameTargetLocked(g.targets[id]) {
		return false
	}
	if w := g.heldWaits[id]; w != nil && w.calls[k] {
		return false
	}
	w := g.heldWaitForLocked(id)
	if w == nil {
		return false
	}
	if g.heldWaitFullLocked(w) {
		g.logf("cdp: refused the answer to Target.getTargetInfo at once: too many calls wait for held iframes target=%s", id)
		return false
	}
	w.calls[k] = true
	g.logf("cdp: holding the answer to Target.getTargetInfo until the guard decides an auto-attached iframe target=%s", id)
	reply := *m
	go g.answerParkedLookup(w.ch, k, reply, res)
	return true
}

func (g *guard) answerParkedLookup(ch <-chan struct{}, k callKey, m message, res map[string]any) {
	<-ch
	g.mu.Lock()
	answer := g.parkedLookupAnswerLocked(k, &m, res)
	flush := g.flushAsync
	g.mu.Unlock()
	if flush != nil {
		flush(nil, [][]byte{answer})
	}
}

func (g *guard) parkedLookupAnswerLocked(k callKey, m *message, res map[string]any) []byte {
	settled, ok := g.parkedAnswers[k]
	delete(g.parkedAnswers, k)
	if k.session != "" && !g.browserSessions[k.session] {
		return missingSession(k.id, k.session)
	}
	if !ok || !settled {
		return missingTarget(k.id, k.session)
	}
	if heldLookupClassifyAtSettle() {
		info, obj := toInfo(res["targetInfo"])
		if heldLookupHideAtSettle() && g.listingHiddenLocked(info) {
			g.logf("cdp: refused a held answer to Target.getTargetInfo once the guard decided its iframe target=%s", info.TargetID)
			return missingTarget(k.id, k.session)
		}
		g.redactInfoLocked(obj, info)
		g.noteWorkerShownLocked(info)
	}
	m.Result = encode(res)
	return encode(m)
}

func (g *guard) expireHeldWait(w *heldWait) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.heldWaits[w.target] != w || w.expired {
		return
	}
	g.logf("cdp: calls waiting for a held iframe reached the hold's deadline target=%s", w.target)
	w.refused = true
	w.expired = true
	g.settleHeldWaitLocked(w, false)
}

func (g *guard) refuseHeldWaitLocked(id string) {
	if w := g.heldWaits[id]; w != nil {
		w.refused = true
	}
}

func (g *guard) settleHeldWaitLocked(w *heldWait, settled bool) {
	for k := range w.calls {
		g.parkedAnswers[k] = settled
	}
	if heldLookupReleaseSlots() {
		w.calls = map[callKey]bool{}
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	select {
	case <-w.ch:
	default:
		close(w.ch)
	}
}

func (g *guard) resolveHeldWaitsLocked() {
	for id, w := range g.heldWaits {
		t := g.targets[id]
		protected := t == nil || t.gone || t.protected
		held := g.iframeHeldLocked(id)
		if w.expired {
			if protected || !held {
				delete(g.heldWaits, id)
			}
			continue
		}
		if !protected && !w.refused && held {
			continue
		}
		if !protected && held {
			g.settleHeldWaitLocked(w, false)
			w.expired = true
			continue
		}
		delete(g.heldWaits, id)
		g.settleHeldWaitLocked(w, t != nil && !t.gone && (t.protected || !w.refused && t.shown))
	}
}

func (g *guard) closeHeldWaits() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, w := range g.heldWaits {
		delete(g.heldWaits, id)
		if !w.expired {
			g.settleHeldWaitLocked(w, false)
		}
	}
}

func (g *guard) parkedAnswerLocked(c *command) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	k := callKey{c.session, c.id}
	settled, ok := g.parkedAnswers[k]
	if !ok {
		return nil, false
	}
	delete(g.parkedAnswers, k)
	if settled {
		return nil, false
	}
	if c.method == "Target.getTargetInfo" {
		return missingTarget(c.id, c.session), true
	}
	return refusal(c.id, c.session, refusedAncestry), true
}
