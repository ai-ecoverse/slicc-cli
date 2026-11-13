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
	t := g.targets[id]
	if t == nil || t.gone || t.typ != "iframe" || t.protected || !g.iframeHeldLocked(id) {
		return nil
	}
	w := g.heldWaits[id]
	if w != nil && w.calls[k] {
		return w.ch
	}
	if w != nil && w.expired {
		return nil
	}
	if w == nil {
		deadline := g.heldDeadlineLocked(id)
		if !time.Now().Before(deadline) {
			return nil
		}
		w = &heldWait{target: id, page: g.heldPageLocked(id), ch: make(chan struct{}), calls: map[callKey]bool{}}
		g.heldWaits[id] = w
		w.timer = time.AfterFunc(time.Until(deadline), func() { g.expireHeldWait(w) })
	}
	perPage, perStream := g.parkedCountsLocked(w.page)
	if len(w.calls) >= maxParkedPerIframe || (w.page != "" && perPage >= maxParkedPerPage) || perStream >= maxParkedPerStream {
		g.parkedAnswers[k] = false
		g.logf("cdp: refused %s at once: too many calls wait for held iframes target=%s", c.method, id)
		return nil
	}
	w.calls[k] = true
	g.logf("cdp: holding %s until the guard decides an auto-attached iframe target=%s", c.method, id)
	return w.ch
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
	w.calls = map[callKey]bool{}
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
