package cdp

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func (g *guard) beginConfirmLocked(nt *navTarget) {
	if nt.confirm == nil || nt.confirm.done {
		nt.confirming = false
		nt.confirm = &navTarget{ready: make(chan struct{}), start: time.Now()}
		nt.confirm.awayAt = g.reads.away(nt.confirm.start)
	}
	if nt.failed {
		nt.confirm.settle(true)
	}
}

func (g *guard) confirmTargetLocked(nt *navTarget) [][]byte {
	check := nt.confirm
	if check == nil || check.done || nt.confirming || nt.session == "" || nt.failed {
		return nil
	}
	nt.confirming = true
	revision := nt.revision
	return [][]byte{g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(m message) [][]byte {
		return g.confirmedTargetLocked(nt, check, revision, m)
	})}
}

func (g *guard) confirmedTargetLocked(nt, check *navTarget, revision int, m message) [][]byte {
	if g.navTargets[nt.target] != nt {
		return nil
	}
	if nt.confirm != check {
		return g.confirmTargetLocked(nt)
	}
	nt.confirming = false
	if check.done {
		return nil
	}
	if revision != nt.revision {
		return g.confirmTargetLocked(nt)
	}
	var result struct {
		FrameTree struct {
			Frame json.RawMessage `json:"frame"`
		} `json:"frameTree"`
	}
	var frame struct {
		ID             string `json:"id"`
		ParentID       string `json:"parentId"`
		URL            string `json:"url"`
		SecurityOrigin string `json:"securityOrigin"`
		UnreachableURL string `json:"unreachableUrl"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || json.Unmarshal(result.FrameTree.Frame, &frame) != nil || frame.ID != nt.target || (frame.ParentID != "" && !nt.iframe) {
		check.settle(true)
		return nil
	}
	t := g.targets[nt.target]
	if t == nil || t.gone {
		check.settle(true)
		return nil
	}
	if g.o.Loaded(frame.URL) || g.o.Loaded(frame.SecurityOrigin) {
		check.settle(false)
		return g.markLocked(t)
	}
	if inheritedDocument(frame.URL, frame.UnreachableURL) {
		if g.o.Target(frame.URL, t.protected, g.openerState(t)) || !g.ordinaryFrameParentLocked(t, frame.ParentID) {
			check.reason = g.frameParentReasonLocked(t, frame.ParentID)
			check.settle(true)
			return nil
		}
		check.settle(false)
		return nil
	}
	if frame.SecurityOrigin == "" {
		check.settle(true)
		return nil
	}
	ev := protect.Evidence{DocumentURL: frame.URL, UnreachableURL: frame.UnreachableURL}
	if protect.ErrorPage(frame.UnreachableURL, ev) && !protect.Inheriting(frame.UnreachableURL) {
		check.settle(false)
		return g.navEventLocked(nt, &message{Method: "Page.frameNavigated", Params: encode(map[string]any{"frame": result.FrameTree.Frame})})
	}
	if frame.URL == "" || protect.Inheriting(frame.URL) || protect.Unparseable(frame.URL) || frame.UnreachableURL != "" {
		check.settle(true)
		return nil
	}
	check.settle(false)
	return nil
}

func inheritedDocument(url, unreachable string) bool {
	return url != "" && protect.Inheriting(url) && unreachable == ""
}

func inheritedParentID(t *target, parentID string) string {
	if parentID != "" || t.typ == "iframe" {
		return parentID
	}
	if t.openerFrame != "" {
		return t.openerFrame
	}
	return t.opener
}

func (g *guard) inheritedParentLocked(t *target, parentID string) *target {
	id := inheritedParentID(t, parentID)
	if parent := g.targets[id]; parent != nil {
		return parent
	}
	return g.frameOwnerLocked(id, t.ctx)
}

func (g *guard) frameParentReasonLocked(t *target, parentID string) string {
	parent := g.inheritedParentLocked(t, parentID)
	if parent == nil && t.openerFrame != "" && inheritedParentID(t, parentID) == t.openerFrame {
		if opener := g.targets[t.opener]; opener != nil && opener.gone {
			parent = opener
		}
	}
	if parent != nil && parent.gone {
		return "inherited document has a gone opener or parent"
	}
	if parent == nil || g.navTargets[parent.id] == nil {
		return "inherited document has an unknown opener or parent; slicc never confirmed it"
	}
	return ""
}

func (g *guard) ordinaryOpenerFrameLocked(owner, frame string) bool {
	if g.framesProtectedLocked(owner) {
		return false
	}
	if shown := g.shown[owner]; shown != nil && !shown.full {
		for _, context := range shown.contexts {
			if context.defaultWorld && context.frame == frame {
				return true
			}
		}
	}
	return false
}

func (g *guard) ordinaryFrameParentLocked(t *target, parentID string) bool {
	if inheritedParentID(t, parentID) == "" {
		return t.typ != "iframe"
	}
	parent := g.inheritedParentLocked(t, parentID)
	if parent == nil || parent.gone || parent.protected || g.decide(parent, parent.protected) || g.targetUnconfirmedLocked(parent.id) {
		return false
	}
	if id := inheritedParentID(t, parentID); g.targets[id] == nil && !g.ordinaryOpenerFrameLocked(parent.id, id) {
		return false
	}
	nt := g.navTargets[parent.id]
	return nt != nil && nt.done && !nt.failed && !nt.historyLocked
}

func (g *guard) confirmReasonLocked(nt *navTarget) string {
	if nt.confirm.reason != "" {
		return refusedGuard + ": " + nt.confirm.reason
	}
	return refusedGuard
}

func (g *guard) targetReasonLocked(id string) string {
	if g.targetProtectedLocked(id) || g.workerConcealedLocked(id) {
		return refusedTarget
	}
	if t := g.targets[id]; t != nil && workerTypes[t.typ] && !t.shown && g.workerUnresolvedLocked(t) {
		return refusedTarget
	}
	if g.targetUnconfirmedLocked(id) {
		return g.confirmReasonLocked(g.navTargets[id])
	}
	return ""
}

type touchReservation struct {
	target string
	seq    int
}

func (g *guard) closeTargetReasonLocked(id string) string {
	if id == "" {
		return ""
	}
	if g.hostTargetLocked(id) {
		g.logf("cdp: closeTarget refused target=%s why=host", id)
		return refusedTarget
	}
	t := g.targets[id]
	if t == nil || t.gone {
		return g.targetReasonLocked(id)
	}
	switch t.typ {
	case "page", "tab":
		if g.targetProtectedLocked(id) && !g.streamClosableLocked(id) {
			why := "not-touched-or-opened"
			if g.openedIneligible[id] {
				why = "opened-ineligible"
			} else if _, ok := g.openedSeenAt[id]; ok {
				why = "seen-not-opened"
			}
			g.logf("cdp: closeTarget refused target=%s type=%s why=%s touched=%t opened=%t seenAt=%d touchSeq=%d opener=%s openerFrame=%s", id, t.typ, why, g.navTargets[id] != nil, g.opened[id], g.openedSeenAt[id], g.touchSeq, t.opener, t.openerFrame)
			return refusedTarget
		}
		return ""
	case "browser":
		g.logf("cdp: closeTarget refused target=%s why=browser", id)
		return refusedTarget
	}
	g.logf("cdp: closeTarget refused target=%s type=%s why=non-page", id, t.typ)
	return g.targetReasonLocked(id)
}

func (g *guard) streamClosableLocked(id string) bool {
	return g.navTargets[id] != nil || g.opened[id]
}

func openedSeenBound() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(OpenedSeenBoundEnv)))
	if err != nil || n <= 0 {
		return maxOpenedSeen
	}
	return min(n, maxOpenedSeen)
}

func (g *guard) noteStreamOpenedLocked(info targetInfo) {
	if info.TargetID == "" || g.listed[info.TargetID] || g.opened[info.TargetID] || g.openedIneligible[info.TargetID] {
		return
	}
	typ := info.Type
	if typ == "" {
		if t := g.targets[info.TargetID]; t != nil {
			typ = t.typ
		}
	}
	if typ != "page" && typ != "tab" {
		return
	}
	first := false
	if _, seen := g.openedSeenAt[info.TargetID]; !seen {
		if g.targets[info.TargetID] != nil {
			g.openedIneligible[info.TargetID] = true
			delete(g.openedWait, info.TargetID)
			g.logf("cdp: stream-opened ineligible known-without-sight target=%s type=%s touchSeq=%d", info.TargetID, typ, g.touchSeq)
			return
		}
		if !g.markOpenedSeenLocked(info.TargetID) {
			g.openedIneligible[info.TargetID] = true
			delete(g.openedWait, info.TargetID)
			g.logf("cdp: stream-opened ineligible sight-bound target=%s type=%s touchSeq=%d bound=%d", info.TargetID, typ, g.touchSeq, openedSeenBound())
			return
		}
		first = true
	}
	parent := info.OpenerID
	if parent == "" {
		parent = info.OpenerFrameID
	}
	if parent == "" {
		if first {
			g.logf("cdp: stream-opened first sight target=%s type=%s opener= openerFrame= seenAt=%d touchSeq=%d openerTouched=false", info.TargetID, typ, g.openedSeenAt[info.TargetID], g.touchSeq)
		}
		return
	}
	parentTouch, ok := g.touchedAt[parent]
	if first {
		g.logf("cdp: stream-opened first sight target=%s type=%s opener=%s openerFrame=%s seenAt=%d touchSeq=%d openerTouched=%t parentTouch=%d", info.TargetID, typ, info.OpenerID, info.OpenerFrameID, g.openedSeenAt[info.TargetID], g.touchSeq, ok, parentTouch)
	}
	if !ok {
		g.openedWait[info.TargetID] = parent
		return
	}
	if parentTouch > g.openedSeenAt[info.TargetID] {
		g.openedIneligible[info.TargetID] = true
		delete(g.openedWait, info.TargetID)
		g.logf("cdp: stream-opened ineligible target=%s opener=%s seenAt=%d parentTouch=%d", info.TargetID, parent, g.openedSeenAt[info.TargetID], parentTouch)
		return
	}
	delete(g.openedWait, info.TargetID)
	g.markStreamOpenedLocked(info.TargetID)
	g.logf("cdp: stream-opened marked target=%s opener=%s seenAt=%d parentTouch=%d", info.TargetID, parent, g.openedSeenAt[info.TargetID], parentTouch)
}

func (g *guard) forgetOpenedSeenLocked(id string) {
	if id == "" {
		return
	}
	delete(g.openedSeenAt, id)
	delete(g.openedIneligible, id)
	delete(g.openedWait, id)
	kept := g.openedSeenOrder[:0]
	for _, old := range g.openedSeenOrder {
		if old != id {
			kept = append(kept, old)
		}
	}
	g.openedSeenOrder = kept
}

func (g *guard) trimOpenedSeenLocked() {
	kept := g.openedSeenOrder[:0]
	for _, old := range g.openedSeenOrder {
		t := g.targets[old]
		if t != nil && !t.gone {
			kept = append(kept, old)
			continue
		}
		delete(g.openedSeenAt, old)
		delete(g.openedIneligible, old)
		delete(g.openedWait, old)
	}
	g.openedSeenOrder = kept
}

func (g *guard) markOpenedSeenLocked(id string) bool {
	if id == "" {
		return false
	}
	if _, ok := g.openedSeenAt[id]; ok {
		return true
	}
	g.trimOpenedSeenLocked()
	if len(g.openedSeenOrder) >= openedSeenBound() {
		return false
	}
	g.openedSeenAt[id] = g.touchSeq
	g.openedSeenOrder = append(g.openedSeenOrder, id)
	return true
}

func (g *guard) trimTouchedLocked() {
	kept := g.touchedOrder[:0]
	for _, old := range g.touchedOrder {
		t := g.targets[old]
		if t != nil && !t.gone {
			kept = append(kept, old)
			continue
		}
		delete(g.touchedAt, old)
	}
	g.touchedOrder = kept
}

func (g *guard) noteWireTouchLocked(k callKey, id string) {
	if id == "" {
		return
	}
	if _, ok := g.touchedAt[id]; ok {
		return
	}
	if _, ok := g.pendingTouch[k]; ok {
		return
	}
	g.touchSeq++
	g.pendingTouch[k] = touchReservation{target: id, seq: g.touchSeq}
}

func (g *guard) dropPendingTouchLocked(k callKey) {
	delete(g.pendingTouch, k)
}

func (g *guard) pendingTouchSeqLocked(id string) (int, bool) {
	best := 0
	found := false
	for _, r := range g.pendingTouch {
		if r.target != id {
			continue
		}
		if !found || r.seq < best {
			best = r.seq
			found = true
		}
	}
	return best, found
}

func (g *guard) clearPendingTouchForTargetLocked(id string) {
	for k, r := range g.pendingTouch {
		if r.target == id {
			delete(g.pendingTouch, k)
		}
	}
}

func (g *guard) preserveOpenedSeenLocked(id string) bool {
	seenAt, ok := g.openedSeenAt[id]
	if !ok {
		return false
	}
	parent, waiting := g.openedWait[id]
	if !waiting || parent == "" {
		return false
	}
	if seq, ok := g.pendingTouchSeqLocked(parent); ok && seq <= seenAt {
		return true
	}
	if touch, ok := g.touchedAt[parent]; ok && touch <= seenAt {
		return true
	}
	return false
}

func (g *guard) markTouchedLocked(id string) {
	if id == "" {
		return
	}
	seq, pending := g.pendingTouchSeqLocked(id)
	g.clearPendingTouchForTargetLocked(id)
	if _, ok := g.touchedAt[id]; ok {
		return
	}
	if !pending {
		g.touchSeq++
		seq = g.touchSeq
	}
	g.touchedAt[id] = seq
	g.touchedOrder = append(g.touchedOrder, id)
	g.trimTouchedLocked()
	g.logf("cdp: stream touch target=%s touchSeq=%d", id, seq)
	g.recheckOpenedWaitLocked(id)
}

func (g *guard) recheckOpenedWaitLocked(opener string) {
	parentTouch := g.touchedAt[opener]
	for id, wait := range g.openedWait {
		if wait != opener || g.opened[id] || g.openedIneligible[id] {
			continue
		}
		seenAt := g.openedSeenAt[id]
		if parentTouch > seenAt {
			g.openedIneligible[id] = true
			delete(g.openedWait, id)
			g.logf("cdp: stream-opened ineligible on touch target=%s opener=%s seenAt=%d parentTouch=%d", id, opener, seenAt, parentTouch)
			continue
		}
		delete(g.openedWait, id)
		g.markStreamOpenedLocked(id)
		g.logf("cdp: stream-opened marked on touch target=%s opener=%s seenAt=%d parentTouch=%d", id, opener, seenAt, parentTouch)
	}
}

func (g *guard) markStreamOpenedLocked(id string) {
	if id == "" || g.opened[id] {
		return
	}
	g.markTouchedLocked(id)
	g.opened[id] = true
	g.openedOrder = append(g.openedOrder, id)
	for len(g.openedOrder) > maxTombstones {
		old := g.openedOrder[0]
		g.openedOrder = g.openedOrder[1:]
		delete(g.opened, old)
	}
}

func (g *guard) sessionUnconfirmedLocked(id string) bool {
	s := g.sessions[id]
	return s != nil && g.targetUnconfirmedLocked(s.target)
}

func (g *guard) targetUnconfirmedLocked(id string) bool {
	nt := g.navTargets[id]
	return nt != nil && nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)
}
