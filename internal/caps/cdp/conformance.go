package cdp

import (
	"encoding/json"
	"strings"
)

const refusedHistory = "the target's history may restore a protected document"

var navigationControls = map[string]bool{
	"Page.navigate": true, "Page.reload": true, "Page.stopLoading": true,
	"Page.close": true, "Page.bringToFront": true, "Page.navigateToHistoryEntry": true,
	"Page.getNavigationHistory": true, "Page.resetNavigationHistory": true,
	"Page.getFrameTree": true, "Page.getResourceTree": true,
	"Target.detachFromTarget": true, "Target.closeTarget": true,
	"Target.getTargetInfo": true, "Target.activateTarget": true,
}

func (g *guard) conformanceLocked(c *command) string {
	s := g.sessions[c.session]
	if s == nil || !navTypes[s.typ] {
		return ""
	}
	nt := g.navTargets[s.target]
	if nt == nil || !nt.historyLocked {
		return ""
	}
	if navigationControls[c.method] && (c.method != "Page.reload" || c.params["scriptToEvaluateOnLoad"] == nil) {
		return ""
	}
	domain, verb, _ := strings.Cut(c.method, ".")
	if continueOK[c.method] || (errorPageDomains[domain] && (verb == "enable" || verb == "disable")) {
		return ""
	}
	return refusedHistory
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
