package cdp

import (
	"encoding/json"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func docConfirmURLClass(url string) string {
	if url == "" {
		return "empty"
	}
	lower := strings.ToLower(url)
	switch {
	case lower == "about:blank" || strings.HasPrefix(lower, "about:blank"):
		return "blank"
	case lower == "about:srcdoc" || strings.HasPrefix(lower, "about:srcdoc"):
		return "about:srcdoc"
	case strings.HasPrefix(lower, "chrome-error:"):
		return "chrome-error"
	case strings.Contains(lower, "new-tab-page"):
		return "chrome://new-tab-page"
	case strings.Contains(lower, "newtab"):
		return "chrome://newtab"
	case strings.HasPrefix(lower, "chrome-extension:"):
		return "extension"
	case strings.HasPrefix(lower, "blob:"):
		return "blob"
	case strings.HasPrefix(lower, "filesystem:"):
		return "filesystem"
	case strings.HasPrefix(lower, "view-source:"):
		return "view-source"
	case protect.Unparseable(url):
		return "unparseable"
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "ordinary"
	}
	if protect.Inheriting(url) {
		return "ordinary"
	}
	return "other"
}

func docConfirmOriginClass(origin string, o *protect.Origins) string {
	if origin == "" || origin == "://" || origin == "null" {
		return "opaque"
	}
	if o != nil && o.Loaded(origin) {
		return "protected"
	}
	s := protect.Scheme(origin)
	if s == "http" || s == "https" || s == "" {
		return "ordinary"
	}
	return "other"
}

func docConfirmProvenanceLabel(created, opened, errorOrdinary bool) string {
	switch {
	case created:
		return "created"
	case opened:
		return "opened"
	case errorOrdinary:
		return "errorProofOrdinary"
	default:
		return "none"
	}
}

func (g *guard) docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin string) string {
	if frameURL == "" && frameOrigin == "" {
		return "park-timeout"
	}
	url := frameURL
	if url == "" {
		return "park-timeout"
	}
	if g.o != nil && g.o.Loaded(url) {
		return "refuse-latch"
	}
	lowerProt := strings.ToLower(url)
	if !chromeErrorDocument(url) && (strings.HasPrefix(lowerProt, "view-source:") || strings.HasPrefix(lowerProt, "blob:") || strings.HasPrefix(lowerProt, "filesystem:")) {
		return "refuse-latch"
	}
	lower := strings.ToLower(url)
	blank := lower == "about:blank" || strings.HasPrefix(lower, "about:blank")
	if blank {
		switch docConfirmOriginClass(frameOrigin, g.o) {
		case "protected":
			return "refuse-latch"
		case "opaque", "ordinary":
			return "admit"
		default:
			return "refuse-nonlatch"
		}
	}
	if chromeErrorDocument(url) || protect.Unparseable(url) {
		return "refuse-nonlatch"
	}
	if strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:") {
		return "refuse-nonlatch"
	}
	if protect.Inheriting(url) {
		return "admit"
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "admit"
	}
	return "refuse-nonlatch"
}

func (g *guard) docObserveSessionLocked(session string) bool {
	return session != "" && g.docObserveSessions[session] != ""
}

func (g *guard) docObservePendingLocked(target string) bool {
	return target != "" && g.docObservePending[target]
}

func (g *guard) docConfirmObserveFilterEventLocked(session, target string) bool {
	if !docConfirmObserveEnabled() || !docConfirmObserveFilterEnabled() {
		return false
	}
	if g.docObserveSessionLocked(session) {
		return true
	}
	if session != "" && g.sessions[session] != nil {
		return false
	}
	if target != "" && (g.docObservePendingLocked(target) || g.docObserveDone[target]) {
		if session != "" {
			if g.docObserveSessions == nil {
				g.docObserveSessions = map[string]string{}
			}
			g.docObserveSessions[session] = target
		}
		return true
	}
	return false
}

func (g *guard) scheduleDocConfirmObserveLocked(target, clientSession, path string) [][]byte {
	if !docConfirmObserveEnabled() || target == "" || g.relaySession == "" {
		return nil
	}
	if g.docObservePending == nil {
		g.docObservePending = map[string]bool{}
	}
	if g.docObservePending[target] || g.docObserveDone[target] {
		return nil
	}
	g.docObservePending[target] = true
	announced := ""
	if t := g.targets[target]; t != nil {
		announced = t.url
	}
	opened := g.opened[target]
	errorOrdinary := g.errorProofOfLocked(target) == errorProofOrdinary
	return [][]byte{g.callbackLocked(g.relaySession, "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, func(m message) [][]byte {
		return g.docConfirmObserveAttachedLocked(target, clientSession, path, announced, opened, errorOrdinary, m)
	})}
}

func (g *guard) docConfirmObserveAttachedLocked(target, clientSession, path, announced string, opened, errorOrdinary bool, m message) [][]byte {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || res.SessionID == "" {
		delete(g.docObservePending, target)
		g.logDocConfirmObserveLocked("would-refuse", docConfirmURLClass(announced), path, "other", "other", false, opened, errorOrdinary, "park-timeout")
		return nil
	}
	if g.docObserveSessions == nil {
		g.docObserveSessions = map[string]string{}
	}
	g.docObserveSessions[res.SessionID] = target
	sid := res.SessionID
	return [][]byte{g.callbackLocked(sid, "Page.getFrameTree", nil, func(tree message) [][]byte {
		return g.docConfirmObserveTreeLocked(target, clientSession, path, announced, opened, errorOrdinary, sid, tree)
	})}
}

func (g *guard) docConfirmObserveTreeLocked(target, clientSession, path, announced string, opened, errorOrdinary bool, proofSession string, m message) [][]byte {
	delete(g.docObservePending, target)
	if g.docObserveDone == nil {
		g.docObserveDone = map[string]bool{}
	}
	g.docObserveDone[target] = true
	frameURL := ""
	frameOrigin := ""
	if m.Error == nil {
		var result struct {
			FrameTree struct {
				Frame struct {
					URL            string `json:"url"`
					SecurityOrigin string `json:"securityOrigin"`
					UnreachableURL string `json:"unreachableUrl"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		if json.Unmarshal(m.Result, &result) == nil {
			frameURL = result.FrameTree.Frame.URL
			if frameURL == "" {
				frameURL = result.FrameTree.Frame.UnreachableURL
			}
			frameOrigin = result.FrameTree.Frame.SecurityOrigin
		}
	}
	announceClass := docConfirmURLClass(announced)
	frameClass := docConfirmURLClass(frameURL)
	if frameURL == "" && frameOrigin == "" && m.Error != nil {
		frameClass = "other"
	}
	originClass := docConfirmOriginClass(frameOrigin, g.o)
	verdict := g.docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin)
	if m.Error != nil {
		verdict = "park-timeout"
	}
	kind := "would-refuse"
	if verdict == "admit" {
		kind = "would-admit"
	}
	g.logDocConfirmObserveLocked(kind, announceClass, path, frameClass, originClass, false, opened, errorOrdinary, verdict)
	var send [][]byte
	if proofSession != "" {
		if g.docObserveSessions == nil {
			g.docObserveSessions = map[string]string{}
		}
		g.docObserveSessions[proofSession] = target
		msg, _ := g.newCallLocked(g.relaySession, "Target.detachFromTarget", map[string]any{"sessionId": proofSession}, false)
		send = append(send, msg)
	}
	_ = clientSession
	return send
}

func (g *guard) logDocConfirmObserveLocked(kind, announceClass, path, frameClass, originClass string, created, opened, errorOrdinary bool, verdict string) {
	line := "cdp: DIAG document-confirm " + kind +
		" class=" + announceClass +
		" path=" + path +
		" frame_class=" + frameClass +
		" origin_class=" + originClass +
		" provenance=" + docConfirmProvenanceLabel(created, opened, errorOrdinary) +
		" verdict=" + verdict
	g.logf("%s", line)
	if g.docObserveLog == nil {
		g.docObserveLog = []string{}
	}
	g.docObserveLog = append(g.docObserveLog, line)
}

func (g *guard) docConfirmObservePathLocked(target, typ string) string {
	if typ == "iframe" {
		return "nested"
	}
	if g.errorProofOfLocked(target) != errorProofNone {
		return "G"
	}
	if g.explicit[target] > 0 {
		return "explicit"
	}
	for _, t := range g.attachCalls {
		if t == target {
			return "explicit"
		}
	}
	if g.opened[target] || g.errorProofOfLocked(target) == errorProofOrdinary {
		return "provenance"
	}
	return "D"
}

func (g *guard) noteDocConfirmObserveLocked(target, path string) {}

func (g *guard) tryLogDocConfirmObserveLocked(target string, atBound bool) {}

func (g *guard) fallthroughDocConfirmObserveLocked(target string) {}

func (g *guard) docConfirmObserveAfterCommitLocked(nt *navTarget, before int) {}

func (g *guard) docConfirmObserveEvidenceLocked(target string) (string, string, bool) {
	return "", "", false
}
