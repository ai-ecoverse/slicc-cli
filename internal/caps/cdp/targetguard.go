package cdp

import (
	"encoding/json"
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
	if parent != nil && parent.gone {
		return "inherited document has a gone opener or parent"
	}
	if parent == nil || g.navTargets[parent.id] == nil {
		return "inherited document has an unknown opener or parent; slicc never confirmed it"
	}
	return ""
}

func (g *guard) ordinaryFrameParentLocked(t *target, parentID string) bool {
	if inheritedParentID(t, parentID) == "" {
		return t.typ != "iframe"
	}
	parent := g.inheritedParentLocked(t, parentID)
	if parent == nil || parent.gone || parent.protected || g.decide(parent, parent.protected) || g.targetUnconfirmedLocked(parent.id) {
		return false
	}
	if g.targets[inheritedParentID(t, parentID)] == nil && g.framesProtectedLocked(parent.id) {
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
	if g.targetProtectedLocked(id) {
		return refusedTarget
	}
	if g.targetUnconfirmedLocked(id) {
		return g.confirmReasonLocked(g.navTargets[id])
	}
	return ""
}

func (g *guard) sessionUnconfirmedLocked(id string) bool {
	s := g.sessions[id]
	return s != nil && g.targetUnconfirmedLocked(s.target)
}

func (g *guard) targetUnconfirmedLocked(id string) bool {
	nt := g.navTargets[id]
	return nt != nil && nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)
}
