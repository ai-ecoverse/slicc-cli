package cdp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/coder/websocket"
)

const refusedHistory = "the target's history may restore a protected document"

var navigationControls = map[string]bool{
	"Page.navigate": true, "Page.reload": true, "Page.stopLoading": true,
	"Page.close": true, "Page.bringToFront": true, "Page.navigateToHistoryEntry": true,
	"Page.getNavigationHistory": true, "Page.resetNavigationHistory": true,
	"Target.detachFromTarget": true, "Target.closeTarget": true,
	"Target.getTargetInfo": true, "Target.activateTarget": true,
}

func (g *guard) conformanceSession(c *command) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[c.session]
	if s == nil || !navTypes[s.typ] {
		return ""
	}
	if navigationControls[c.method] && (c.method != "Page.reload" || c.params["scriptToEvaluateOnLoad"] == nil) {
		return ""
	}
	domain, verb, _ := strings.Cut(c.method, ".")
	if continueOK[c.method] || (errorPageDomains[domain] && (verb == "enable" || verb == "disable")) {
		return ""
	}
	nt := g.navTargets[s.target]
	if nt == nil || !nt.done || nt.failed {
		return ""
	}
	return nt.session
}

func (rl *relay) commandEvidence(ctx context.Context, session, method string) (message, bool) {
	msg, call := rl.g.call(session, method, nil)
	defer rl.g.cancel(call)
	if rl.conn.Write(ctx, websocket.MessageText, msg) != nil {
		return message{}, false
	}
	select {
	case m := <-call.ch:
		return m, m.Error == nil
	case <-ctx.Done():
	case <-rl.s.Done():
	}
	return message{}, false
}

func (rl *relay) conform(c *command) string {
	session := rl.g.conformanceSession(c)
	if session == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), HistoryTimeout)
	defer cancel()
	history, ok := rl.commandEvidence(ctx, session, "Page.getNavigationHistory")
	if !ok {
		return refusedHistory
	}
	if reason := rl.g.historyConformance(session, history); reason != "" {
		return reason
	}
	tree, ok := rl.commandEvidence(ctx, session, "Page.getFrameTree")
	if !ok {
		return refusedGuard
	}
	return rl.g.frameEvidence(tree)
}

func (g *guard) historyConformance(session string, m message) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	nt := g.navSessions[session]
	if nt == nil {
		return refusedGuard
	}
	if g.historyEvidence(m) != "" {
		nt.historyLocked = true
	}
	if nt.historyLocked {
		return refusedHistory
	}
	return ""
}

func (g *guard) historyEvidence(m message) string {
	var h struct {
		CurrentIndex *int `json:"currentIndex"`
		Entries      []struct {
			URL          *string `json:"url"`
			UserTypedURL string  `json:"userTypedURL"`
		} `json:"entries"`
	}
	if json.Unmarshal(m.Result, &h) != nil || h.CurrentIndex == nil || *h.CurrentIndex < 0 || *h.CurrentIndex >= len(h.Entries) {
		return refusedHistory
	}
	for _, e := range h.Entries {
		if e.URL == nil || *e.URL == "" || g.o.Loaded(*e.URL) || g.o.Loaded(e.UserTypedURL) {
			return refusedHistory
		}
	}
	return ""
}

func (g *guard) frameEvidence(m message) string {
	type frame struct {
		ID             string  `json:"id"`
		URL            *string `json:"url"`
		SecurityOrigin *string `json:"securityOrigin"`
		UnreachableURL string  `json:"unreachableUrl"`
	}
	type frameTree struct {
		Frame       frame       `json:"frame"`
		ChildFrames []frameTree `json:"childFrames"`
	}
	var tree struct {
		FrameTree frameTree `json:"frameTree"`
	}
	if json.Unmarshal(m.Result, &tree) != nil {
		return refusedGuard
	}
	var check func(frameTree) string
	check = func(t frameTree) string {
		f := t.Frame
		if f.ID == "" || f.URL == nil || *f.URL == "" || f.SecurityOrigin == nil {
			return refusedGuard
		}
		if g.o.Loaded(*f.URL) || g.o.Loaded(*f.SecurityOrigin) || g.o.Loaded(f.UnreachableURL) {
			return refusedTarget
		}
		for _, child := range t.ChildFrames {
			if reason := check(child); reason != "" {
				return reason
			}
		}
		return ""
	}
	return check(tree.FrameTree)
}
