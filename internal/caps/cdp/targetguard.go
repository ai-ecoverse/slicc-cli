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
	var frame confirmFrame
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || json.Unmarshal(result.FrameTree.Frame, &frame) != nil || frame.ID != nt.target || (frame.ParentID != "" && !nt.iframe) {
		check.settle(true)
		return nil
	}
	t := g.targets[nt.target]
	if t == nil || t.gone {
		check.settle(true)
		return nil
	}
	if nt.iframe {
		if send, blocked := g.confirmIframeParentLocked(t, frame.ParentID); blocked {
			check.reason = g.frameParentReasonLocked(t, frame.ParentID)
			check.settle(true)
			return send
		}
	}
	return g.confirmFrameDocumentLocked(nt, check, t, frame, result.FrameTree.Frame)
}

type confirmFrame struct {
	ID             string `json:"id"`
	ParentID       string `json:"parentId"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"securityOrigin"`
	UnreachableURL string `json:"unreachableUrl"`
}

func (g *guard) confirmFrameDocumentLocked(nt, check *navTarget, t *target, frame confirmFrame, raw json.RawMessage) [][]byte {
	g.noteFrameKeptLocked(nt, frame.URL, frame.SecurityOrigin)
	g.noteErrorPageSnapshotLocked(nt, frame.URL, frame.SecurityOrigin, frame.UnreachableURL)
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
		return g.navEventLocked(nt, &message{Method: "Page.frameNavigated", Params: encode(map[string]any{"frame": raw})})
	}
	if frame.URL == "" || protect.Inheriting(frame.URL) || protect.Unparseable(frame.URL) || frame.UnreachableURL != "" {
		check.settle(true)
		return nil
	}
	check.settle(false)
	return nil
}

func (g *guard) confirmIframeParentLocked(t *target, parentID string) ([][]byte, bool) {
	if t == nil || t.typ != "iframe" {
		return nil, false
	}
	if parentID == "" {
		return nil, false
	}
	send, _ := g.noteFrameParentLocked(t.id, parentID)
	if t.parentFrame == "" {
		t.parentFrame = parentID
	}
	unknown := g.iframeAncestryUnknownLocked(t) || g.iframeAncestorUnknownLocked(t)
	navPending := g.iframeAncestorNavPendingLocked(t) || g.targetHeldLocked(parentID) || g.targetUnconfirmedLocked(parentID)
	protected := g.iframeFromProtectedAncestorLocked(t) || unknown
	if !protected && !navPending {
		parent := g.targets[parentID]
		switch {
		case parent == nil:
			protected = true
		case parent.protected || g.decide(parent, parent.protected):
			protected = true
		case parent.typ == "iframe" && g.iframeAncestryUnknownLocked(parent):
			protected = true
		case g.creatorProtectedLocked(parentID):
			protected = true
		}
	}
	if navPending && !protected {
		g.logf("cdp: iframe confirm blocked on pending ancestor target=%s parent=%s %s", t.id, parentID, g.ancestorProtectStateLocked(parentID))
		return send, true
	}
	if !protected {
		return send, false
	}
	prev := t.protected
	t.protected = true
	if !prev {
		send = append(send, g.protectLocked(t)...)
	}
	return send, true
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
	if t := g.targets[id]; t != nil && t.typ == "iframe" && !g.extensionFrameTargetLocked(t) && !t.protected && (g.iframeAncestryUnknownLocked(t) || g.iframeAncestorPendingLocked(t) || g.iframeConcealedLocked(id)) {
		return refusedAncestry
	}
	if g.targetProtectedLocked(id) || g.workerConcealedLocked(id) || g.iframeConcealedLocked(id) {
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

const closeProofPending = "\x00close-proof"

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
		if g.closeNeedsProofLocked(id) {
			return closeProofPending
		}
		return g.closeTargetFinalLocked(id)
	case "browser":
		g.logf("cdp: closeTarget refused target=%s why=browser", id)
		return refusedTarget
	}
	g.logf("cdp: closeTarget refused target=%s type=%s why=non-page", id, t.typ)
	return g.targetReasonLocked(id)
}

func (g *guard) closeTargetFinalLocked(id string) string {
	if g.keptCloseLocked(id) {
		g.logf("cdp: closeTarget refused target=%s type=%s why=kept", id, g.targets[id].typ)
		return refusedTarget
	}
	return ""
}

func (g *guard) streamClosableLocked(id string) bool {
	return g.navTargets[id] != nil || g.opened[id]
}

func (g *guard) closeNeedsProofLocked(id string) bool {
	nt := g.navTargets[id]
	if nt == nil || nt.session == "" {
		return false
	}
	navigating := nt.unsure || len(nt.loading) > 0
	if !navigating || nt.navigationURL != "" {
		return false
	}
	t := g.targets[id]
	documentURL := ""
	if t != nil {
		documentURL = t.url
	}
	if nt.documentURL != "" {
		documentURL = nt.documentURL
	}
	if g.recordedProtectedErrorPageLocked(nt, documentURL) {
		return false
	}
	return true
}

func (g *guard) keptCloseLocked(id string) bool {
	t := g.targets[id]
	if t == nil || t.gone {
		return false
	}
	nt := g.navTargets[id]
	documentURL := t.url
	if nt != nil && nt.documentURL != "" {
		documentURL = nt.documentURL
	}
	if g.recordedProtectedErrorPageLocked(nt, documentURL) && !g.navigationAfterErrorLocked(nt) {
		return false
	}
	navigating := nt != nil && (nt.unsure || len(nt.loading) > 0)
	if navigating && (nt.navigationURL == "" || g.o.Kept(nt.navigationURL)) {
		return true
	}
	if g.o.Kept(t.url) || g.o.Kept(documentURL) {
		return true
	}
	return g.keptInheritedLocked(t, documentURL)
}

func (g *guard) recordedProtectedErrorPageLocked(nt *navTarget, documentURL string) bool {
	if chromeErrorDocument(documentURL) {
		return true
	}
	return nt != nil && nt.errorURL != "" && g.o.Loaded(nt.errorURL)
}

func (g *guard) navigationAfterErrorLocked(nt *navTarget) bool {
	if nt == nil || !nt.errorRecorded {
		return false
	}
	return nt.navGen > nt.errorNavGen && (nt.unsure || len(nt.loading) > 0)
}

func (g *guard) noteErrorPageSnapshotLocked(nt *navTarget, url, origin, unreachable string) {
	if nt == nil {
		return
	}
	if !chromeErrorDocument(url) && (unreachable == "" || !g.o.Loaded(unreachable)) {
		return
	}
	if url != "" {
		nt.documentURL = url
	}
	if origin != "" {
		nt.securityOrigin = origin
	}
	if unreachable != "" {
		nt.errorURL = unreachable
	}
	nt.errorRecorded = true
	nt.errorNavGen = nt.navGen
}

func (g *guard) applyCloseProofLocked(id string, m message, gen, rev int) bool {
	nt := g.navTargets[id]
	if nt == nil || m.Error != nil || nt.navGen != gen || nt.revision != rev {
		return false
	}
	var result struct {
		FrameTree struct {
			Frame struct {
				ID             string `json:"id"`
				ParentID       string `json:"parentId"`
				URL            string `json:"url"`
				SecurityOrigin string `json:"securityOrigin"`
				UnreachableURL string `json:"unreachableUrl"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if json.Unmarshal(m.Result, &result) != nil {
		return false
	}
	frame := result.FrameTree.Frame
	if frame.ID != nt.target || (frame.ParentID != "" && !nt.iframe) {
		return false
	}
	if nt.navGen != gen || nt.revision != rev {
		return false
	}
	g.noteFrameKeptLocked(nt, frame.URL, frame.SecurityOrigin)
	if chromeErrorDocument(frame.URL) || frame.UnreachableURL != "" && g.o.Loaded(frame.UnreachableURL) {
		if frame.URL != "" {
			nt.documentURL = frame.URL
		}
		if frame.SecurityOrigin != "" {
			nt.securityOrigin = frame.SecurityOrigin
		}
		if frame.UnreachableURL != "" {
			nt.errorURL = frame.UnreachableURL
		}
		nt.errorRecorded = true
		nt.errorNavGen = gen
	}
	return true
}

func chromeErrorDocument(raw string) bool {
	return strings.HasPrefix(raw, "chrome-error:") || raw == "chrome-error://chromewebdata/"
}

func (g *guard) keptInheritedLocked(t *target, documentURL string) bool {
	if t == nil {
		return false
	}
	inheriting := protect.Inheriting(documentURL) || opaqueOrNonHTTPDocument(documentURL)
	nt := g.navTargets[t.id]
	if nt != nil {
		for _, origin := range []string{nt.securityOrigin, nt.keptOrigin} {
			if origin != "" && (g.o.KeptInherited(documentURL, origin) || g.o.KeptInherited(t.url, origin)) {
				return true
			}
		}
	}
	if g.keptURLLatch[t.id] && inheriting {
		return true
	}
	if !inheriting && documentURL != "" && !protect.Inheriting(t.url) && t.url != "" {
		return false
	}
	for _, id := range []string{t.opener, t.openerFrame} {
		if id == "" {
			continue
		}
		c := g.targets[id]
		if c == nil || c.gone {
			continue
		}
		if g.o.KeptInherited(documentURL, c.url) || g.o.KeptInherited(t.url, c.url) {
			return true
		}
	}
	return false
}

func opaqueOrNonHTTPDocument(raw string) bool {
	return protect.Inheriting(raw) || raw == "" || strings.HasPrefix(raw, "about:") || (!strings.HasPrefix(raw, "http:") && !strings.HasPrefix(raw, "https:") && raw != "")
}

func (g *guard) noteFrameKeptLocked(nt *navTarget, url, origin string) {
	if nt == nil {
		return
	}
	if origin != "" {
		nt.securityOrigin = origin
	}
	if url != "" {
		nt.documentURL = url
	}
	g.noteKeptLatchLocked(nt)
}

func (g *guard) noteKeptLatchLocked(nt *navTarget) {
	if nt == nil {
		return
	}
	doc := nt.documentURL
	origin := nt.securityOrigin
	if chromeErrorDocument(doc) {
		return
	}
	if g.o.Kept(doc) || g.o.Kept(origin) || origin != "" && g.o.KeptInherited(doc, origin) {
		if origin != "" {
			nt.keptOrigin = origin
		}
		g.keptURLLatch[nt.target] = true
		return
	}
	if doc == "" || protect.Inheriting(doc) || opaqueOrNonHTTPDocument(doc) {
		return
	}
	nt.keptOrigin = ""
	delete(g.keptURLLatch, nt.target)
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
	g.trimTouchedLocked()
	g.touchedAt[id] = seq
	g.touchedOrder = append(g.touchedOrder, id)
	g.logf("cdp: stream touch target=%s touchSeq=%d", id, seq)
	g.recheckOpenedWaitLocked(id, seq)
}

func (g *guard) recheckOpenedWaitLocked(opener string, parentTouch int) {
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
	g.latchOpenedKeptLocked(id)
	for len(g.openedOrder) > maxTombstones {
		old := g.openedOrder[0]
		g.openedOrder = g.openedOrder[1:]
		delete(g.opened, old)
		delete(g.keptURLLatch, old)
	}
}

func (g *guard) latchOpenedKeptLocked(id string) {
	t := g.targets[id]
	if t == nil {
		return
	}
	if g.o.Kept(t.url) {
		g.keptURLLatch[id] = true
		return
	}
	if !protect.Inheriting(t.url) && t.url != "" && !opaqueOrNonHTTPDocument(t.url) {
		return
	}
	for _, oid := range []string{t.openerFrame, t.opener} {
		if oid == "" {
			continue
		}
		c := g.targets[oid]
		if c == nil || c.gone {
			continue
		}
		origin := c.url
		if ont := g.navTargets[oid]; ont != nil {
			if ont.keptOrigin != "" {
				origin = ont.keptOrigin
			} else if ont.securityOrigin != "" {
				origin = ont.securityOrigin
			} else if ont.documentURL != "" {
				origin = ont.documentURL
			}
		}
		if g.o.Kept(origin) || g.o.Kept(c.url) || g.keptURLLatch[oid] || g.o.KeptInherited(t.url, origin) || g.o.KeptInherited(t.url, c.url) {
			g.keptURLLatch[id] = true
			if nt := g.navTargets[id]; nt != nil {
				nt.keptOrigin = origin
			}
			return
		}
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
