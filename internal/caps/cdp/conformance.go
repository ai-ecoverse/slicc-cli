package cdp

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
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
	if g.historySetupLocked(c, s, nt) {
		return ""
	}
	return refusedHistory
}

var historySetupKeys = map[string]map[string]bool{
	"Network.setBypassServiceWorker": {"bypass": true},
	"Target.setAutoAttach":           {"autoAttach": true, "flatten": true, "waitForDebuggerOnStart": true, "filter": true},
}

var historySetupFilter = []any{map[string]any{"type": "iframe", "exclude": false}, map[string]any{"exclude": true}}

func (g *guard) noteSetupProtectedLocked(nt *navTarget) {
	if nt == nil || !historySetupLatchEnabled() {
		return
	}
	nt.setupProtectedSeen = true
	nt.setupProtectedSeq = g.confirmSeq
}

func (g *guard) noteSetupProtectedFromTargetLocked(nt *navTarget, t *target, v urlVerdict) {
	if nt == nil || t == nil || !navTypes[t.typ] || nt.failed {
		return
	}
	if v.unparseable || g.o.Loaded(t.url) {
		g.noteSetupProtectedLocked(nt)
	}
}

func (g *guard) clearSetupProtectedLocked(nt *navTarget, issuedSeq int64) {
	if nt == nil || !nt.setupProtectedSeen {
		return
	}
	if issuedSeq <= nt.setupProtectedSeq {
		return
	}
	if g.o.Loaded(nt.navigationURL) || g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.securityOrigin) || (nt.errorURL != "" && g.o.Loaded(nt.errorURL)) {
		return
	}
	if len(nt.loading) != 0 || nt.unsure {
		return
	}
	nt.setupProtectedSeen = false
	nt.setupProtectedSeq = 0
}

func (g *guard) noteSetupProtectedNavLocked(nt *navTarget, m *message) {
	if nt == nil {
		return
	}
	if g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.securityOrigin) || g.o.Loaded(nt.navigationURL) || (nt.errorURL != "" && g.o.Loaded(nt.errorURL)) {
		g.noteSetupProtectedLocked(nt)
		return
	}
	if m != nil && (m.Method == "Page.frameStartedNavigating" || m.Method == "Page.frameNavigated") {
		var p navCommitParams
		if json.Unmarshal(m.Params, &p) != nil {
			g.noteSetupProtectedLocked(nt)
			return
		}
		if m.Method == "Page.frameNavigated" && p.Frame.ID == nt.target && (p.Frame.ParentID == "" || nt.iframe) && (p.Frame.URL == "" || p.Frame.SecurityOrigin == "") {
			g.noteSetupProtectedLocked(nt)
			return
		}
	}
	if m == nil || m.Method != "Fetch.requestPaused" {
		return
	}
	var p struct {
		FrameID      string `json:"frameId"`
		ResourceType string `json:"resourceType"`
		Request      struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		g.noteSetupProtectedLocked(nt)
		return
	}
	if p.ResourceType == "Document" && nt.documentLocks(p.FrameID) && g.o.Loaded(p.Request.URL) {
		g.noteSetupProtectedLocked(nt)
	}
}

func (g *guard) noteSetupProtectedConfirmLocked(nt *navTarget, t *target, frame confirmFrame) {
	if nt == nil {
		return
	}
	if g.o.Loaded(frame.URL) || g.o.Loaded(frame.SecurityOrigin) || g.o.Loaded(frame.UnreachableURL) {
		g.noteSetupProtectedLocked(nt)
		return
	}
	if t != nil && g.o.Loaded(t.url) {
		g.noteSetupProtectedLocked(nt)
		return
	}
	if frame.URL == "" || protect.Unparseable(frame.URL) || frame.SecurityOrigin == "" {
		g.noteSetupProtectedLocked(nt)
	}
}

func (g *guard) historySetupLocked(c *command, s *session, nt *navTarget) bool {
	if !historySetupAdmitEnabled() {
		return false
	}
	if s.typ != "page" || g.browserSessions[c.session] || !nt.done || nt.failed {
		return false
	}
	if nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed) {
		return false
	}
	if nt.errorURL != "" || g.errorPageSessionLocked(c.session) {
		return false
	}
	if nt.setupProtectedSeen {
		return false
	}
	keys, ok := historySetupKeys[c.method]
	if !ok {
		return false
	}
	for key := range c.params {
		if !keys[key] {
			return false
		}
	}
	switch c.method {
	case "Network.setBypassServiceWorker":
		return c.params["bypass"] == true
	}
	wait, waitNamed := c.params["waitForDebuggerOnStart"]
	filter, filterNamed := c.params["filter"]
	return c.params["autoAttach"] == true && c.params["flatten"] == true && (!waitNamed || wait == false) && filterNamed && reflect.DeepEqual(filter, historySetupFilter)
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

func (g *guard) historyCurrentProtected(m message) bool {
	var h struct {
		CurrentIndex *int `json:"currentIndex"`
		Entries      []struct {
			URL          *string `json:"url"`
			UserTypedURL string  `json:"userTypedURL"`
		} `json:"entries"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &h) != nil || h.CurrentIndex == nil || h.Entries == nil {
		return false
	}
	i := *h.CurrentIndex
	if i < 0 || i >= len(h.Entries) {
		return false
	}
	e := h.Entries[i]
	return (e.URL != nil && g.o.Loaded(*e.URL)) || g.o.Loaded(e.UserTypedURL)
}

const maxInitialUnknowns = 3

func (g *guard) settleInitialProtectedLocked(nt *navTarget) [][]byte {
	nt.historyLocked = true
	g.noteSetupProtectedLocked(nt)
	nt.settle(false)
	return nil
}

func (g *guard) initialHistoryLocked(nt *navTarget, res message) [][]byte {
	g.rememberHistoryRefusals(nt, res)
	state := g.historyStatus(res)
	currentProtected := g.historyCurrentProtected(res)
	if state == historyProtected {
		nt.historyLocked = true
	}
	return [][]byte{g.callbackLocked(g.relaySession, "Target.getTargetInfo", map[string]any{"targetId": nt.target}, func(info message) [][]byte {
		var result struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		infoBad := info.Error != nil || json.Unmarshal(info.Result, &result) != nil || result.TargetInfo.TargetID != nt.target
		url := ""
		if !infoBad {
			url = result.TargetInfo.URL
		}
		if infoBad || g.o.Loaded(url) {
			return g.settleInitialProtectedLocked(nt)
		}
		if currentProtected {
			nt.historyLocked = true
			g.noteSetupProtectedLocked(nt)
		}
		if chromeErrorDocument(url) && (nt.historyLocked || state == historyClean) {
			if nt.historyLocked {
				g.noteSetupProtectedLocked(nt)
			}
			nt.settle(false)
			return nil
		}
		if !nt.historyLocked && state == historyClean && url != "" {
			nt.settle(false)
			return nil
		}
		g.logf("cdp: confirming a target's initial frame while its setup evidence is incomplete")
		return [][]byte{g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(frame message) [][]byte {
			return g.initialFrameTreeLocked(nt, frame, state)
		})}
	})}
}

func (g *guard) initialFrameTreeLocked(nt *navTarget, frame message, hist historyState) [][]byte {
	chromeErrFresh := g.chromeErrorFrameMessage(frame) && (nt.historyLocked || hist == historyClean)
	switch g.initialFrameStatus(frame, nt.target) {
	case historyProtected:
		return g.settleInitialProtectedLocked(nt)
	case historyUnknown:
		if !chromeErrFresh {
			return g.deferInitialLocked(nt)
		}
	}
	if nt.historyLocked {
		if chromeErrFresh {
			g.noteSetupProtectedLocked(nt)
		}
		nt.settle(false)
		return nil
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
		if chromeErrFresh && nt.historyLocked {
			g.noteSetupProtectedLocked(nt)
		}
		nt.settle(false)
		return nil
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
	if f.ID != target || f.URL == nil || *f.URL == "" || f.SecurityOrigin == nil || *f.SecurityOrigin == "" || chromeErrorDocument(*f.URL) {
		return historyUnknown
	}
	return historyClean
}

func (g *guard) chromeErrorFrameMessage(m message) bool {
	var tree struct {
		FrameTree struct {
			Frame struct {
				URL *string `json:"url"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	return json.Unmarshal(m.Result, &tree) == nil && tree.FrameTree.Frame.URL != nil && chromeErrorDocument(*tree.FrameTree.Frame.URL)
}

func (g *guard) deferInitialLocked(nt *navTarget) [][]byte {
	if nt.done {
		return nil
	}
	if nt.historyLocked {
		nt.initialUnknowns++
		if nt.initialUnknowns >= maxInitialUnknowns {
			g.noteSetupProtectedLocked(nt)
			nt.settle(false)
			return nil
		}
		if g.scheduleInitial != nil {
			g.scheduleInitial(nt)
			return nil
		}
		g.noteSetupProtectedLocked(nt)
		nt.settle(false)
		return nil
	}
	if g.scheduleInitial != nil {
		g.scheduleInitial(nt)
		return nil
	}
	nt.historyLocked = true
	g.noteSetupProtectedLocked(nt)
	nt.settle(false)
	return nil
}

func (g *guard) retryInitial(nt *navTarget) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if nt.done || g.lost || g.navTargets[nt.target] != nt {
		return nil
	}
	if nt.historyLocked {
		return g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(frame message) [][]byte {
			return g.initialFrameTreeLocked(nt, frame, historyUnknown)
		})
	}
	return g.callbackLocked(nt.session, "Page.getNavigationHistory", nil, func(res message) [][]byte { return g.initialHistoryLocked(nt, res) })
}
