package cdp

import (
	"net/url"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type errorProofState int

const (
	errorProofNone errorProofState = iota
	errorProofPending
	errorProofOrdinary
	errorProofProtected
	errorProofFailed
)

func (g *guard) errorProofOfLocked(id string) errorProofState {
	if g.errorProof == nil {
		return errorProofNone
	}
	return g.errorProof[id]
}

func (g *guard) errorProofGenOfLocked(id string) int {
	if g.errorProofGen == nil {
		return 0
	}
	return g.errorProofGen[id]
}

func (g *guard) setErrorProofLocked(id string, st errorProofState) {
	if id == "" {
		return
	}
	if g.errorProof == nil {
		g.errorProof = map[string]errorProofState{}
	}
	if st == errorProofNone {
		delete(g.errorProof, id)
		delete(g.errorTitles, id)
		return
	}
	g.errorProof[id] = st
}

func (g *guard) resetFailedLoadProofLocked(id string) {
	if id == "" {
		return
	}
	if g.errorProofGen == nil {
		g.errorProofGen = map[string]int{}
	}
	g.errorProofGen[id]++
	g.setErrorProofLocked(id, errorProofNone)
	if nt := g.navTargets[id]; nt != nil {
		if nt.confirm != nil && !nt.confirm.done {
			nt.confirm.settle(true)
		}
		nt.confirming = false
		nt.revision++
	}
}

func (g *guard) resetFailedLoadProofOnNavLocked(nt *navTarget, method string) {
	if nt == nil || (method != "Page.frameStartedNavigating" && method != "Page.frameNavigated") {
		return
	}
	st := g.errorProofOfLocked(nt.target)
	switch {
	case st == errorProofOrdinary || st == errorProofProtected || st == errorProofFailed:
		g.resetFailedLoadProofLocked(nt.target)
	case st == errorProofPending && method == "Page.frameStartedNavigating":
		g.resetFailedLoadProofLocked(nt.target)
	}
}

func (g *guard) noteErrorTitleLocked(id, title string) {
	if id == "" || title == "" {
		return
	}
	if g.errorTitles == nil {
		g.errorTitles = map[string]string{}
	}
	if g.errorTitles[id] == "" {
		g.errorTitles[id] = title
	}
}

func (g *guard) failedLoadNeedsProofLocked(t *target) bool {
	if t == nil || t.gone {
		return false
	}
	if t.typ != "page" && t.typ != "tab" {
		return false
	}
	if !chromeErrorDocument(t.url) {
		return false
	}
	switch g.errorProofOfLocked(t.id) {
	case errorProofOrdinary, errorProofProtected, errorProofFailed:
		return false
	default:
		return true
	}
}

func (g *guard) blankFailedLoadInfoLocked(obj map[string]any, info targetInfo) bool {
	if obj == nil {
		return false
	}
	title := titleField(obj)
	t := g.targets[info.TargetID]
	if t != nil && chromeErrorDocument(t.url) {
		if prev := g.errorTitles[t.id]; prev != "" && title != "" && title != prev {
			g.resetFailedLoadProofLocked(t.id)
			g.setErrorProofLocked(t.id, errorProofPending)
		}
	}
	if t == nil {
		if !chromeErrorDocument(info.URL) || (info.Type != "page" && info.Type != "tab" && info.Type != "") {
			return false
		}
		g.noteErrorTitleLocked(info.TargetID, title)
		obj["url"] = ""
		obj["title"] = ""
		return true
	}
	st := g.errorProofOfLocked(t.id)
	switch st {
	case errorProofPending, errorProofFailed:
	case errorProofProtected:
		g.noteErrorTitleLocked(t.id, title)
		obj["title"] = ""
		return true
	default:
		if !g.failedLoadNeedsProofLocked(t) {
			return false
		}
	}
	g.noteErrorTitleLocked(t.id, title)
	obj["url"] = ""
	obj["title"] = ""
	return true
}

func titleField(obj map[string]any) string {
	s, _ := obj["title"].(string)
	return s
}

func (g *guard) armFailedLoadProofLocked(t *target) [][]byte {
	if t == nil || !g.failedLoadNeedsProofLocked(t) {
		return nil
	}
	g.setErrorProofLocked(t.id, errorProofPending)
	if g.relaySession == "" {
		return nil
	}
	if nt := g.navTargets[t.id]; nt != nil {
		g.beginConfirmLocked(nt)
		return g.confirmTargetLocked(nt)
	}
	return g.touchLocked(t.id, t.typ)
}

func (g *guard) holdFailedLoadAttachLocked(m *message, pSession string, waiting bool, info targetInfo, send [][]byte) (browserVerdict, bool) {
	if info.Type != "page" && info.Type != "tab" {
		return browserVerdict{}, false
	}
	if !chromeErrorDocument(info.URL) {
		return browserVerdict{}, false
	}
	t := g.targets[info.TargetID]
	if t == nil {
		return browserVerdict{}, false
	}
	switch g.errorProofOfLocked(t.id) {
	case errorProofFailed, errorProofProtected:
		h := &heldSession{id: pSession, parent: m.SessionID, target: info.TargetID, typ: info.Type, waiting: waiting, at: time.Now()}
		part := g.hideHeldPageAndTabsLocked(h, true, "failed-load protected")
		return browserVerdict{send: append(send, part.send...), page: part.page}, true
	case errorProofOrdinary:
		return browserVerdict{}, false
	}
	g.noteErrorTitleLocked(t.id, attachTitle(m))
	g.setErrorProofLocked(t.id, errorProofPending)
	params := g.attachedHoldParams(m, pSession, waiting, info)
	if obj, _ := params["targetInfo"].(map[string]any); obj != nil {
		g.blankFailedLoadInfoLocked(obj, info)
	}
	send = append(send, g.holdLocked(m.SessionID, params, info, waiting)...)
	send = append(send, g.armFailedLoadProofLocked(t)...)
	return browserVerdict{send: send, hold: []string{pSession}}, true
}

func attachTitle(m *message) string {
	params := decodeParams(m.Params)
	if params == nil {
		return ""
	}
	obj, _ := params["targetInfo"].(map[string]any)
	return titleField(obj)
}

func (g *guard) keepHeldForFailedLoadLocked(h *heldSession, info any) bool {
	t := g.targets[h.target]
	if t == nil || !g.failedLoadNeedsProofLocked(t) {
		return false
	}
	if h.params != nil && info != nil {
		if obj, ok := info.(map[string]any); ok {
			h.params["targetInfo"] = obj
			g.noteErrorTitleLocked(h.target, titleField(obj))
			g.blankFailedLoadInfoLocked(obj, targetInfo{TargetID: h.target, Type: h.typ, URL: t.url})
		}
	}
	g.setErrorProofLocked(h.target, errorProofPending)
	return true
}

func (g *guard) heldFailedLoadLocked(targetID string) bool {
	for _, h := range g.held {
		if h != nil && h.target == targetID {
			return true
		}
	}
	return g.errorProofOfLocked(targetID) == errorProofPending
}

func ordinaryFailedUnreachable(o *protect.Origins, raw string) bool {
	if raw == "" || o == nil || protect.Unparseable(raw) || protect.Inheriting(raw) {
		return false
	}
	s := protect.Scheme(raw)
	if s != "http" && s != "https" {
		return false
	}
	if protect.Host(raw) == "" {
		return false
	}
	if u, err := url.Parse(raw); err != nil || u.User != nil {
		return false
	}
	return !o.Loaded(raw)
}

func (g *guard) finishFailedLoadAfterConfirmLocked(t *target, frame confirmFrame) [][]byte {
	if t == nil || (t.typ != "page" && t.typ != "tab") || !chromeErrorDocument(frame.URL) {
		return nil
	}
	if !g.heldFailedLoadLocked(t.id) {
		return nil
	}
	if !ordinaryFailedUnreachable(g.o, frame.UnreachableURL) {
		if frame.UnreachableURL != "" && g.o.Loaded(frame.UnreachableURL) {
			g.setErrorProofLocked(t.id, errorProofProtected)
			g.blankHeldFailedLoadTitleLocked(t.id)
			return g.finishFailedLoadHeldLocked(t.id, true)
		}
		g.setErrorProofLocked(t.id, errorProofFailed)
		return g.finishFailedLoadHeldLocked(t.id, true)
	}
	g.setErrorProofLocked(t.id, errorProofOrdinary)
	if title := g.errorTitles[t.id]; title != "" {
		for _, h := range g.held {
			if h == nil || h.target != t.id || h.params == nil {
				continue
			}
			if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
				obj["title"] = title
				obj["url"] = t.url
			}
		}
	}
	return g.finishFailedLoadHeldLocked(t.id, false)
}

func (g *guard) blankHeldFailedLoadTitleLocked(targetID string) {
	for _, h := range g.held {
		if h == nil || h.target != targetID || h.params == nil {
			continue
		}
		if obj, _ := h.params["targetInfo"].(map[string]any); obj != nil {
			obj["title"] = ""
			if t := g.targets[targetID]; t != nil {
				obj["url"] = t.url
			}
		}
	}
}

func (g *guard) finishFailedLoadHeldLocked(targetID string, hide bool) [][]byte {
	var send [][]byte
	ids := make([]string, 0, len(g.held))
	for sid, h := range g.held {
		if h != nil && h.target == targetID {
			ids = append(ids, sid)
		}
	}
	for _, sid := range ids {
		h := g.held[sid]
		if h == nil {
			continue
		}
		delete(g.held, sid)
		g.noteDocConfirmObserveLocked(targetID, "G")
		if hide {
			part := g.hideHeldPageAndTabsLocked(h, true, "failed-load protected")
			send = append(send, part.send...)
			g.pendingPage = append(g.pendingPage, part.page...)
			g.tryLogDocConfirmObserveLocked(targetID, false)
			continue
		}
		part := g.deliverHeldLocked(h)
		send = append(send, part.send...)
		g.pendingPage = append(g.pendingPage, part.page...)
	}
	return send
}
