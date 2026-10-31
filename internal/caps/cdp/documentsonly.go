package cdp

import (
	"encoding/json"
	"strings"
)

const (
	documentsOnlySignal       = "documents-only"
	refusedDocumentsCommand   = "this browser shows slicc no frames, so slicc allows only the commands measured not to reach one; read the page with Runtime.evaluate instead"
	refusedDocumentsSelection = "this browser shows slicc no frames, so a command can't name a frame or an execution context here"
	refusedNeedsPage          = "this command needs a page's session; attach to a tab with Target.attachToTarget and send it there"
)

var pageDomains = commandSet(
	"Page", "Runtime", "DOM", "DOMSnapshot", "DOMDebugger", "DOMStorage", "CSS", "Input", "Accessibility", "Emulation",
	"Overlay", "Debugger", "HeapProfiler", "Profiler", "Console", "Log", "LayerTree", "Animation", "Audits",
	"Media", "WebAudio", "Cast", "HeadlessExperimental", "IndexedDB", "CacheStorage", "ServiceWorker", "Performance",
	"Inspector", "Memory", "Slicc",
)

var documentsPageCommands = commandSet(
	"Page.enable", "Page.disable", "Page.setLifecycleEventsEnabled", "Page.navigate", "Page.reload", "Page.stopLoading",
	"Page.getNavigationHistory", "Page.navigateToHistoryEntry", "Page.getFrameTree", "Page.getLayoutMetrics",
	"Runtime.enable", "Runtime.disable", "Runtime.evaluate", "Runtime.callFunctionOn", "Runtime.getProperties",
	"Runtime.releaseObject", "Runtime.releaseObjectGroup", "Runtime.runIfWaitingForDebugger",
	"DOM.enable", "DOM.disable", "DOM.getDocument", "DOM.querySelector", "DOM.querySelectorAll", "DOM.describeNode",
	"DOM.resolveNode", "DOM.requestNode", "DOM.getBoxModel", "DOM.getContentQuads", "DOM.focus",
	"Accessibility.enable", "Accessibility.disable", "Accessibility.getFullAXTree",
	"Input.dispatchMouseEvent", "Input.dispatchKeyEvent", "Input.insertText",
	"Network.enable", "Network.getCookies", "Network.getAllCookies",
	"Network.setCookie", "Network.setCookies", "Network.deleteCookies",
	"Log.enable", "Log.disable", "Security.enable", "Security.disable", "Performance.enable", "Performance.disable",
	"Inspector.enable", "Inspector.disable", "Browser.getWindowForTarget",
)

var documentsBrowserCommands = commandSet(
	"Browser.getVersion", "Browser.getWindowForTarget",
	"Target.getTargets", "Target.getTargetInfo", "Target.setDiscoverTargets", "Target.setAutoAttach",
	"Target.attachToTarget", "Target.attachToBrowserTarget", "Target.detachFromTarget", "Target.createTarget",
	"Target.closeTarget", "Target.activateTarget",
)

var documentsSelectors = []string{"frameId", "contextId", "uniqueContextId", "executionContextId", "executionContextName", "pierce"}

func commandSet(methods ...string) map[string]bool {
	set := make(map[string]bool, len(methods))
	for _, method := range methods {
		set[method] = true
	}
	return set
}

func documentsOnlyRefusal(raw json.RawMessage) bool {
	var e struct {
		Data string `json:"data"`
	}
	return json.Unmarshal(raw, &e) == nil && e.Data == documentsOnlySignal
}

func holdsDocuments(raw json.RawMessage) bool {
	var r struct {
		HoldsDocuments bool `json:"holdsDocuments"`
	}
	return json.Unmarshal(raw, &r) == nil && r.HoldsDocuments
}

func (g *guard) documentRequestPatterns() []any {
	var out []any
	for _, pattern := range g.o.DocumentPatterns() {
		out = append(out, map[string]any{"urlPattern": pattern, "requestStage": "Request", "resourceType": "Document"})
	}
	return out
}

func (g *guard) fetchStepLocked(nt *navTarget, session string, step func(m message) [][]byte) func(m message) [][]byte {
	return func(m message) [][]byte {
		if m.Error == nil || !documentsOnlyRefusal(m.Error) {
			return step(m)
		}
		nt.documentsAsked = true
		g.logf("cdp: the browser pauses only documents here: %s", m.Error)
		return [][]byte{g.callbackLocked(session, "Fetch.enable", map[string]any{"patterns": g.documentRequestPatterns()}, func(res message) [][]byte {
			if res.Error == nil && !holdsDocuments(res.Result) {
				res.Error = json.RawMessage(`{"message":"the browser didn't say it holds documents"}`)
			}
			nt.documentsOnly = res.Error == nil
			g.documentsOnly = g.documentsOnly || nt.documentsOnly
			return step(res)
		})}
	}
}

func (nt *navTarget) documentLocks(frame string) bool {
	return frame == nt.target || frame == "" || nt.documentsAsked
}

func (g *guard) contentRuleLocked(c *command) string {
	if reason := g.scopeRuleLocked(c); reason != "" {
		return reason
	}
	if reason := g.conformanceLocked(c); reason != "" {
		return reason
	}
	return g.documentsOnlyRuleLocked(c)
}

func (g *guard) scopeRuleLocked(c *command) string {
	domain, _, _ := strings.Cut(c.method, ".")
	if (c.session == "" || g.browserSessions[c.session]) && pageDomains[domain] {
		return refusedNeedsPage
	}
	return ""
}

func (g *guard) documentsPageLocked(id string) bool {
	s := g.sessions[id]
	if s == nil || s.typ != "page" || g.browserSessions[id] {
		return false
	}
	nt := g.navTargets[s.target]
	return nt != nil && nt.documentsOnly
}

func (g *guard) documentsOnlyRuleLocked(c *command) string {
	if !g.documentsOnly {
		return ""
	}
	if !g.documentsPageLocked(c.session) {
		if documentsBrowserCommands[c.method] {
			return ""
		}
		return refusedDocumentsCommand
	}
	if !documentsPageCommands[c.method] {
		return refusedDocumentsCommand
	}
	for _, selector := range documentsSelectors {
		if _, named := c.params[selector]; named {
			return refusedDocumentsSelection
		}
	}
	return ""
}
