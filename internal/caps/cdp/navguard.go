package cdp

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	refusedGuard     = "slicc can't keep seven's origin out of this target"
	refusedErrorPage = "the target shows Chrome's error page for a protected URL, which names it; slicc only lets a page navigate, reload or close it"
	refusedOutlived  = "the target committed Chrome's error page for a protected URL while this command ran, so slicc dropped its answer"
)

var slowGuard = 2 * time.Second

var navTypes = map[string]bool{"page": true, "iframe": true, "background_page": true, "webview": true}

var sameDocument = map[string]bool{"sameDocument": true, "historySameDocument": true}

const (
	maxLoading = 64
	maxFailed  = 16
)

var workerTypes = map[string]bool{"worker": true, "shared_worker": true, "service_worker": true}

var unheldStart = map[string]bool{"shared_worker": true, "service_worker": true}

func guarded(typ string) bool {
	return navTypes[typ] || workerTypes[typ]
}

func (g *guard) startsUnheldLocked(c *command) bool {
	s, ok := g.sessions[c.session]
	return ok && c.method == "Runtime.runIfWaitingForDebugger" && unheldStart[s.typ]
}

func (g *guard) startsUnheld(c *command) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startsUnheldLocked(c)
}

type navTarget struct {
	target           string
	session          string
	start            time.Time
	progress         time.Time
	awayAt           time.Duration
	reason           string
	ready            chan struct{}
	pending          int
	done             bool
	failed           bool
	loading          map[string]int
	started          int
	unsure           bool
	errorURL         string
	documentURL      string
	failedURLs       []string
	commits          int
	failCommit       int
	worker           bool
	shared           bool
	historyLocked    bool
	protectedHistory map[string]string
	confirm          *navTarget
	confirming       bool
	revision         int
}

func (nt *navTarget) settle(failed bool) {
	if nt.done {
		return
	}
	nt.done, nt.failed = true, failed
	close(nt.ready)
}

func (g *guard) protectedRequestPatterns() []any {
	return []any{map[string]any{"urlPattern": "*", "requestStage": "Request"}}
}

func (g *guard) callbackLocked(sessionID, method string, params any, fn func(m message) [][]byte) []byte {
	msg, c := g.newCallLocked(sessionID, method, params, false)
	c.fn = fn
	return msg
}

func (g *guard) touchLocked(targetID, typ string) [][]byte {
	if targetID == "" || !guarded(typ) || g.relaySession == "" {
		return nil
	}
	if _, ok := g.navTargets[targetID]; ok {
		return nil
	}
	nt := &navTarget{target: targetID, ready: make(chan struct{}), start: time.Now(), worker: workerTypes[typ], shared: typ == "shared_worker"}
	nt.awayAt = g.reads.away(nt.start)
	g.navTargets[targetID] = nt
	sent := time.Now()
	msg := g.callbackLocked(g.relaySession, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, func(m message) [][]byte {
		if took := time.Since(sent); took > slowGuard {
			g.logf("cdp: attaching a target's navigation guard took %s", took.Round(time.Millisecond))
		}
		return g.navAttachedLocked(nt, m)
	})
	return [][]byte{msg}
}

func (g *guard) navAttachedLocked(nt *navTarget, m message) [][]byte {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || res.SessionID == "" {
		g.logf("cdp: couldn't guard a target's navigations: %s", m.Error)
		nt.settle(true)
		return nil
	}
	nt.session = res.SessionID
	g.movedLocked(nt, time.Now())
	g.navSessions[res.SessionID] = nt
	sent := time.Now()
	step := func(method string) func(m message) [][]byte {
		return func(m message) [][]byte {
			if took := time.Since(sent); took > slowGuard {
				g.logf("cdp: %s for a target's navigation guard took %s", method, took.Round(time.Millisecond))
			}
			return nt.stepLocked(g, m)
		}
	}
	type call struct {
		method string
		params any
	}
	network := call{"Network.enable", map[string]any{"maxTotalBufferSize": 0, "maxResourceBufferSize": 0, "maxPostDataSize": 0}}
	calls := []call{
		{"Fetch.enable", map[string]any{"patterns": g.patterns}},
		network,
		{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
		{"Runtime.enable", nil},
		{"Page.enable", nil},
	}
	if nt.worker {
		calls = []call{network}
	}
	nt.pending = len(calls)
	out := make([][]byte, 0, len(calls))
	for _, c := range calls {
		out = append(out, g.callbackLocked(res.SessionID, c.method, c.params, step(c.method)))
	}
	if nt.confirm != nil {
		out = append(out, g.confirmTargetLocked(nt)...)
	}
	return out
}

func (nt *navTarget) stepLocked(g *guard, m message) [][]byte {
	if m.Error != nil {
		g.logf("cdp: couldn't guard a target's navigations: %s", m.Error)
		nt.settle(true)
		return nil
	}
	if nt.pending--; nt.pending == 0 {
		if nt.worker || (g.targets[nt.target] != nil && g.targets[nt.target].typ == "iframe") {
			nt.settle(false)
			return nil
		}
		return [][]byte{g.callbackLocked(nt.session, "Page.getNavigationHistory", nil, func(res message) [][]byte {
			return g.initialHistoryLocked(nt, res)
		})}
	}
	return nil
}

func (g *guard) navEventLocked(nt *navTarget, m *message) [][]byte {
	if nt.shared && m.Method == "Inspector.targetCrashed" {
		return g.navReleaseLocked(nt)
	}
	g.navWorldLocked(nt, m)
	before := nt.commits
	nt.commitLocked(m)
	if m.Method == "Page.frameStartedNavigating" || m.Method == "Page.frameNavigated" || m.Method == "Page.frameStoppedLoading" {
		nt.revision++
	}
	var send [][]byte
	if nt.confirm != nil && !nt.confirm.done {
		send = g.confirmTargetLocked(nt)
	}
	if nt.commits != before && (g.o.Loaded(nt.documentURL) || g.o.Loaded(nt.errorURL)) {
		nt.historyLocked = true
	}
	if nt.commits != before && g.o.Loaded(nt.errorURL) {
		nt.failCommit = nt.commits
	}
	if g.o.Loaded(nt.errorURL) && !slices.Contains(nt.failedURLs, nt.errorURL) {
		nt.failedURLs = append(nt.failedURLs, nt.errorURL)
		if len(nt.failedURLs) > maxFailed {
			nt.failedURLs = nt.failedURLs[1:]
		}
	}
	if m.Method != "Fetch.requestPaused" {
		return send
	}
	var p struct {
		NetworkID    string `json:"networkId"`
		RequestID    string `json:"requestId"`
		FrameID      string `json:"frameId"`
		ResourceType string `json:"resourceType"`
		Request      struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.RequestID == "" {
		return send
	}
	if g.o.Loaded(p.Request.URL) && (p.ResourceType == "Document" || g.clientOwnedLocked(nt.target, p.NetworkID)) {
		if p.ResourceType == "Document" && (p.FrameID == nt.target || p.FrameID == "") {
			nt.historyLocked = true
		}
		g.logf("cdp: failed a protected document or client-issued request in a target this stream touched")
		msg, _ := g.newCallLocked(nt.session, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": "BlockedByClient"}, false)
		return append(send, msg)
	}
	msg, _ := g.newCallLocked(nt.session, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID}, false)
	return append(send, msg)
}

func (g *guard) navDetachedLocked(sessionID string) {
	nt, ok := g.navSessions[sessionID]
	if !ok {
		return
	}
	delete(g.navSessions, sessionID)
	if !nt.done {
		g.logf("cdp: lost the session guarding a target's navigations before it was ready")
	}
	nt.session = ""
	nt.settle(true)
	nt.failed = true
	if nt.confirm != nil {
		nt.confirm.settle(true)
	}
}

func (g *guard) navReleaseLocked(nt *navTarget) [][]byte {
	sid := nt.session
	g.logf("cdp: let go of the session guarding a shared worker that ended")
	g.navForgetLocked(nt.target)
	msg, _ := g.newCallLocked(g.relaySession, "Target.detachFromTarget", map[string]any{"sessionId": sid}, false)
	return [][]byte{msg}
}

func (g *guard) navForgetLocked(targetID string) {
	nt, ok := g.navTargets[targetID]
	if !ok {
		return
	}
	g.forgetClientTargetLocked(targetID)
	delete(g.navTargets, targetID)
	delete(g.tainted, targetID)
	delete(g.clean, targetID)
	if nt.session != "" {
		delete(g.navSessions, nt.session)
	}
	nt.settle(true)
	if nt.confirm != nil {
		nt.confirm.settle(true)
	}
}

func (g *guard) navWaitLocked(sessionID string) <-chan struct{} {
	s, ok := g.sessions[sessionID]
	if !ok || !guarded(s.typ) {
		return nil
	}
	nt, ok := g.navTargets[s.target]
	if !ok {
		return nil
	}
	if !nt.done {
		return nt.ready
	}
	if nt.confirm != nil && !nt.confirm.done {
		return nt.confirm.ready
	}
	return nil
}

func (g *guard) navCheckLocked(sessionID string) string {
	s, ok := g.sessions[sessionID]
	if !ok || !guarded(s.typ) {
		return ""
	}
	nt, ok := g.navTargets[s.target]
	if ok && nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed) {
		return refusedGuard
	}
	switch {
	case ok && nt.done && !nt.failed:
		return ""
	case !ok:
		g.logf("cdp: refused a command on a target whose navigations aren't guarded")
	case !nt.done:
		g.logf("cdp: refused a command on a target whose navigation guard isn't ready")
	case nt.reason != "":
		return refusedGuard + ": " + nt.reason
	}
	return refusedGuard
}

func (nt *navTarget) commitLocked(m *message) {
	var p struct {
		FrameID        string `json:"frameId"`
		LoaderID       string `json:"loaderId"`
		RequestID      string `json:"requestId"`
		NavigationType string `json:"navigationType"`
		Type           string `json:"type"`
		Frame          struct {
			ID             string `json:"id"`
			ParentID       string `json:"parentId"`
			LoaderID       string `json:"loaderId"`
			URL            string `json:"url"`
			UnreachableURL string `json:"unreachableUrl"`
		} `json:"frame"`
	}
	switch m.Method {
	case "Page.frameStartedNavigating", "Page.frameNavigated", "Page.frameStoppedLoading", "Network.loadingFailed":
	default:
		return
	}
	if json.Unmarshal(m.Params, &p) != nil {
		nt.unsure = true
		return
	}
	switch m.Method {
	case "Page.frameStartedNavigating":
		if p.FrameID != nt.target || sameDocument[p.NavigationType] {
			return
		}
		if nt.loading == nil {
			nt.loading = map[string]int{}
		}
		if p.LoaderID == "" || len(nt.loading) >= maxLoading {
			nt.unsure = true
			return
		}
		nt.started++
		nt.loading[p.LoaderID] = nt.started
	case "Network.loadingFailed":
		delete(nt.loading, p.RequestID)
	case "Page.frameStoppedLoading":
		if p.FrameID == nt.target {
			clear(nt.loading)
			nt.unsure = false
		}
	case "Page.frameNavigated":
		if p.Frame.ID != nt.target || p.Frame.ParentID != "" {
			return
		}
		if p.Type == "BackForwardCacheRestore" {
			clear(nt.loading)
		}
		if n, ok := nt.loading[p.Frame.LoaderID]; ok {
			for l, m := range nt.loading {
				if m <= n {
					delete(nt.loading, l)
				}
			}
		}
		nt.errorURL = p.Frame.UnreachableURL
		nt.documentURL = p.Frame.URL
		nt.commits++
	}
}

func (g *guard) evidenceLocked(t *target) protect.Evidence {
	nt, ok := g.navTargets[t.id]
	if !ok || !nt.done || nt.failed || nt.errorURL == "" {
		return protect.Evidence{}
	}
	return protect.Evidence{DocumentURL: nt.documentURL, UnreachableURL: nt.errorURL, Navigating: nt.unsure || len(nt.loading) > 0}
}

var errorPageMethods = map[string]bool{
	"Page.navigate": true, "Page.reload": true, "Page.stopLoading": true, "Page.close": true, "Page.bringToFront": true,
	"Page.navigateToHistoryEntry": true, "Page.getNavigationHistory": true, "Page.resetNavigationHistory": true,
	"Page.getFrameTree": true, "Page.setLifecycleEventsEnabled": true, "Page.removeScriptToEvaluateOnNewDocument": true,
	"Runtime.runIfWaitingForDebugger": true,
	"Target.setAutoAttach":            true, "Target.detachFromTarget": true, "Target.closeTarget": true,
	"Target.getTargetInfo": true, "Target.activateTarget": true,
}

var errorPageDomains = map[string]bool{"Page": true, "Runtime": true, "Target": true, "Inspector": true, "Network": true, "Log": true, "Fetch": true}

var errorPageEvents = map[string]bool{
	"Runtime.executionContextCreated": true, "Runtime.executionContextDestroyed": true, "Runtime.executionContextsCleared": true,
	"Page.frameNavigated": true, "Page.frameStartedNavigating": true, "Page.frameRequestedNavigation": true,
	"Page.frameScheduledNavigation": true, "Page.frameClearedScheduledNavigation": true, "Page.navigatedWithinDocument": true,
	"Page.frameStartedLoading": true, "Page.frameStoppedLoading": true, "Page.frameAttached": true, "Page.frameDetached": true,
	"Page.frameSubtreeWillBeDetached": true, "Page.frameResized": true, "Page.lifecycleEvent": true, "Page.loadEventFired": true,
	"Page.domContentEventFired": true, "Page.documentOpened": true, "Page.backForwardCacheNotUsed": true, "Page.windowOpen": true,
	"Page.downloadWillBegin": true, "Page.downloadProgress": true, "Page.interstitialShown": true, "Page.interstitialHidden": true,
	"Page.screencastVisibilityChanged": true,
}

func (g *guard) errorPageSessionLocked(sessionID string) bool {
	s, ok := g.sessions[sessionID]
	if !ok {
		return false
	}
	nt, ok := g.navTargets[s.target]
	return ok && nt.errorURL != "" && g.o.Loaded(nt.errorURL)
}

func (g *guard) errorPageCheckLocked(c *command) string {
	if !g.errorPageSessionLocked(c.session) {
		return ""
	}
	if c.method == "Page.reload" && c.params["scriptToEvaluateOnLoad"] != nil {
		return refusedErrorPage
	}
	if errorPageMethods[c.method] || continueOK[c.method] {
		return ""
	}
	domain, verb, _ := strings.Cut(c.method, ".")
	if errorPageDomains[domain] && (verb == "enable" || verb == "disable") {
		return ""
	}
	return refusedErrorPage
}

func errorPageEvent(method string) bool {
	domain, _, _ := strings.Cut(method, ".")
	switch domain {
	case "Target", "Inspector", "Network", "Fetch":
		return true
	}
	return errorPageEvents[method]
}

type commitMark struct {
	nt            *navTarget
	commits       int
	historyLocked bool
}

type failedForm struct {
	form     string
	redacted string
}

func (g *guard) failedFormsLocked(sessionID string) []failedForm {
	s, ok := g.sessions[sessionID]
	if !ok {
		return nil
	}
	nt, ok := g.navTargets[s.target]
	if !ok {
		return nil
	}
	var forms []failedForm
	for _, u := range nt.failedURLs {
		r := redactURL(u)
		for _, f := range []string{u, url.QueryEscape(u), url.PathEscape(u)} {
			forms = append(forms, failedForm{f, r})
		}
		if d, err := url.QueryUnescape(u); err == nil {
			forms = append(forms, failedForm{d, r})
		}
		if d, err := url.PathUnescape(u); err == nil {
			forms = append(forms, failedForm{d, r})
		}
	}
	slices.SortStableFunc(forms, func(a, b failedForm) int { return len(b.form) - len(a.form) })
	return forms
}

func (g *guard) noteCommitLocked(k callKey, c *command) {
	if errorPageMethods[c.method] || continueOK[c.method] {
		return
	}
	s, ok := g.sessions[c.session]
	if !ok {
		return
	}
	if nt, ok := g.navTargets[s.target]; ok {
		g.commitMarks[k] = commitMark{nt: nt, commits: nt.commits, historyLocked: nt.historyLocked}
	}
}

func (g *guard) outlivedLocked(k callKey) bool {
	mk, ok := g.commitMarks[k]
	delete(g.commitMarks, k)
	return ok && (mk.nt.failCommit > mk.commits || (mk.nt.historyLocked && !mk.historyLocked))
}
