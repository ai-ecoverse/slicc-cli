package cdp

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

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
	nt.confirming = false
	if g.navTargets[nt.target] != nt {
		return nil
	}
	if nt.confirm != check {
		return g.confirmTargetLocked(nt)
	}
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
	if m.Error != nil || json.Unmarshal(m.Result, &result) != nil || json.Unmarshal(result.FrameTree.Frame, &frame) != nil || frame.ID != nt.target || frame.ParentID != "" {
		return nil
	}
	t := g.targets[nt.target]
	if t == nil || t.gone {
		return nil
	}
	if g.o.Loaded(frame.URL) || g.o.Loaded(frame.SecurityOrigin) {
		check.settle(false)
		return g.markLocked(t)
	}
	if frame.SecurityOrigin == "" {
		return nil
	}
	ev := protect.Evidence{DocumentURL: frame.URL, UnreachableURL: frame.UnreachableURL}
	if protect.ErrorPage(t.url, ev) && g.o.Loaded(frame.UnreachableURL) {
		check.settle(false)
		return g.navEventLocked(nt, &message{Method: "Page.frameNavigated", Params: encode(map[string]any{"frame": result.FrameTree.Frame})})
	}
	if frame.URL == "" || protect.Inheriting(frame.URL) || protect.Unparseable(frame.URL) || frame.UnreachableURL != "" {
		return nil
	}
	check.settle(false)
	return nil
}

func (g *guard) targetReasonLocked(id string) string {
	if g.targetProtectedLocked(id) {
		return refusedTarget
	}
	if g.targetUnconfirmedLocked(id) {
		return refusedGuard
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
