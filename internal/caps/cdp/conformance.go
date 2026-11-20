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

func (g *guard) rememberHistoryRefusals(nt *navTarget, m message) {
	if nt == nil || m.Error != nil {
		return
	}
	var h struct {
		Entries []struct {
			ID           json.Number `json:"id"`
			URL          string      `json:"url"`
			UserTypedURL string      `json:"userTypedURL"`
		} `json:"entries"`
	}
	if json.Unmarshal(m.Result, &h) != nil {
		return
	}
	for _, entry := range h.Entries {
		id, err := entry.ID.Int64()
		if err != nil || id < 0 || (!g.o.Loaded(entry.URL) && !g.o.Loaded(entry.UserTypedURL)) {
			continue
		}
		if nt.protectedHistory == nil {
			nt.protectedHistory = map[string]string{}
		}
		nt.protectedHistory[entry.ID.String()] = g.destinationReason(entry.URL, entry.UserTypedURL)
		nt.historyLocked = true
	}
}

func (g *guard) historyRefusalLocked(c *command) string {
	s := g.sessions[c.session]
	if s == nil {
		return ""
	}
	nt := g.navTargets[s.target]
	entry, ok := c.params["entryId"].(json.Number)
	if nt != nil && nt.historyLocked && ok {
		return nt.protectedHistory[entry.String()]
	}
	return ""
}

func (g *guard) initialHistoryLocked(nt *navTarget, res message) [][]byte {
	g.rememberHistoryRefusals(nt, res)
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
			switch g.initialFrameStatus(frame, nt.target) {
			case historyProtected:
				nt.historyLocked = true
				nt.settle(false)
				return nil
			case historyUnknown:
				return g.deferInitialLocked(nt)
			}
			return [][]byte{g.callbackLocked(nt.session, "Page.getNavigationHistory", nil, func(fresh message) [][]byte {
				g.rememberHistoryRefusals(nt, fresh)
				switch g.historyStatus(fresh) {
				case historyProtected:
					nt.historyLocked = true
				case historyUnknown:
					if !nt.historyLocked {
						return g.deferInitialLocked(nt)
					}
				}
				nt.settle(false)
				return nil
			})}
		})}
	})}
}

func (g *guard) initialFrameStatus(m message, target string) historyState {
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
		return historyUnknown
	}
	f := tree.FrameTree.Frame
	if (f.URL != nil && g.o.Loaded(*f.URL)) || (f.SecurityOrigin != nil && g.o.Loaded(*f.SecurityOrigin)) || g.o.Loaded(f.UnreachableURL) {
		return historyProtected
	}
	if f.ID != target || f.URL == nil || *f.URL == "" || f.SecurityOrigin == nil || *f.SecurityOrigin == "" {
		return historyUnknown
	}
	return historyClean
}

func (g *guard) deferInitialLocked(nt *navTarget) [][]byte {
	if nt.done {
		return nil
	}
	if g.scheduleInitial != nil {
		g.scheduleInitial(nt)
		return nil
	}
	nt.historyLocked = true
	nt.settle(false)
	return nil
}

func (g *guard) retryInitial(nt *navTarget) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if nt.done || g.lost || g.navTargets[nt.target] != nt {
		return nil
	}
	msg := g.callbackLocked(nt.session, "Page.getNavigationHistory", nil, func(res message) [][]byte { return g.initialHistoryLocked(nt, res) })
	return msg
}
