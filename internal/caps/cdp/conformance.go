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

type historyState int

const (
	historyUnknown historyState = iota
	historyClean
	historyProtected
)

func (g *guard) historyStatus(m message) historyState {
	var h struct {
		CurrentIndex *int `json:"currentIndex"`
		Entries      []struct {
			URL          *string `json:"url"`
			UserTypedURL string  `json:"userTypedURL"`
		} `json:"entries"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &h) != nil {
		return historyUnknown
	}
	state := historyClean
	if h.CurrentIndex == nil || h.Entries == nil || (*h.CurrentIndex < 0 && (*h.CurrentIndex != -1 || len(h.Entries) != 0)) || *h.CurrentIndex >= len(h.Entries) {
		state = historyUnknown
	}
	for _, e := range h.Entries {
		if (e.URL != nil && g.o.Loaded(*e.URL)) || g.o.Loaded(e.UserTypedURL) {
			return historyProtected
		}
		if e.URL == nil || *e.URL == "" {
			state = historyUnknown
		}
	}
	return state
}

func (g *guard) historyEvidence(m message) string {
	if g.historyStatus(m) != historyClean {
		return refusedHistory
	}
	return ""
}

func (g *guard) initialHistoryLocked(nt *navTarget, res message) [][]byte {
	state := g.historyStatus(res)
	if state == historyProtected {
		nt.historyLocked = true
	}
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": nt.target}, func(info message) [][]byte {
		var result struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if info.Error != nil || json.Unmarshal(info.Result, &result) != nil || result.TargetInfo.TargetID != nt.target || g.o.Loaded(result.TargetInfo.URL) {
			nt.historyLocked = true
		}
		if nt.historyLocked || (state == historyClean && result.TargetInfo.URL != "") {
			nt.settle(false)
			return nil
		}
		g.logf("cdp: confirming a target's initial frame while its setup evidence is incomplete")
		return [][]byte{g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(frame message) [][]byte {
			if !g.initialFrameSafe(frame, nt.target) {
				nt.historyLocked = true
				nt.settle(false)
				return nil
			}
			return [][]byte{g.callbackLocked(nt.session, "Page.getNavigationHistory", nil, func(fresh message) [][]byte {
				if g.historyEvidence(fresh) != "" {
					nt.historyLocked = true
				}
				nt.settle(false)
				return nil
			})}
		})}
	})}
}

func (g *guard) initialFrameSafe(m message, target string) bool {
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID             string  `json:"id"`
				URL            *string `json:"url"`
				SecurityOrigin *string `json:"securityOrigin"`
				UnreachableURL string  `json:"unreachableUrl"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &tree) != nil {
		return false
	}
	f := tree.FrameTree.Frame
	return f.ID == target && f.URL != nil && *f.URL != "" && f.SecurityOrigin != nil && *f.SecurityOrigin != "" && !g.o.Loaded(*f.URL) && !g.o.Loaded(*f.SecurityOrigin) && !g.o.Loaded(f.UnreachableURL)
}
