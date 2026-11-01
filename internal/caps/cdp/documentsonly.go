package cdp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	framesWait                = 10 * time.Second
	documentsOnlySignal       = "documents-only"
	refusedDocumentsCommand   = "slicc can't see inside this browser's frames, so it allows only the commands measured not to reach one; read the page with Runtime.evaluate instead"
	refusedDocumentsSelection = "slicc can't see inside this browser's frames, so a command can't name a frame or an execution context here"
	refusedNeedsPage          = "this command needs a page's session; attach to a tab with Target.attachToTarget and send it there"
)

var pageDomains = commandSet(
	"Page", "Runtime", "DOM", "DOMSnapshot", "DOMDebugger", "DOMStorage", "CSS", "Input", "Accessibility", "Emulation",
	"Overlay", "Debugger", "HeapProfiler", "Profiler", "Console", "Log", "LayerTree", "Animation", "Audits",
	"Media", "WebAudio", "Cast", "HeadlessExperimental", "IndexedDB", "CacheStorage", "ServiceWorker", "Performance",
	"Inspector",
)

var documentsPageCommands = commandSet(
	"Page.enable", "Page.disable", "Page.setLifecycleEventsEnabled", "Page.navigate", "Page.reload", "Page.stopLoading",
	"Page.getNavigationHistory", "Page.navigateToHistoryEntry", "Page.getFrameTree", "Page.getLayoutMetrics",
	"Page.createIsolatedWorld",
	"Runtime.enable", "Runtime.disable", "Runtime.evaluate", "Runtime.callFunctionOn", "Runtime.getProperties",
	"Runtime.releaseObject", "Runtime.releaseObjectGroup", "Runtime.runIfWaitingForDebugger",
	"DOM.enable", "DOM.disable", "DOM.getDocument", "DOM.querySelector", "DOM.querySelectorAll", "DOM.describeNode",
	"DOM.resolveNode", "DOM.requestNode", "DOM.getBoxModel", "DOM.getContentQuads", "DOM.focus",
	"Accessibility.enable", "Accessibility.disable", "Accessibility.getFullAXTree",
	"Input.dispatchMouseEvent", "Input.dispatchKeyEvent", "Input.insertText",
	"Fetch.enable", "Fetch.disable", "Network.setBypassServiceWorker", "Network.enable", "Network.getCookies", "Network.getAllCookies",
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

func guardFetch(c *command) bool {
	patterns, _ := c.params["patterns"].([]any)
	if len(patterns) != 1 {
		return false
	}
	p, _ := patterns[0].(map[string]any)
	return len(p) == 3 && p["urlPattern"] == "*" && p["resourceType"] == "Document" && p["requestStage"] == "Request"
}

func acknowledgement(id int64, session string) []byte {
	m := map[string]any{"id": id, "result": map[string]any{}}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (g *guard) answerLocally(c *command) []byte {
	if !g.documentsPageLocked(c.session) {
		return nil
	}
	if strings.HasPrefix(c.method, "Fetch.") || c.method == "Network.setBypassServiceWorker" || (g.verifiesInput && strings.HasPrefix(c.method, "Input.")) {
		return acknowledgement(c.id, c.session)
	}
	if c.method == "Page.createIsolatedWorld" {
		delete(c.params, "grantUniveralAccess")
	}
	return nil
}

type frameEvidence struct {
	Frame *struct {
		URL            *string `json:"url"`
		SecurityOrigin *string `json:"securityOrigin"`
		UnreachableURL *string `json:"unreachableUrl"`
	} `json:"frame"`
	ChildFrames []*frameEvidence `json:"childFrames"`
}

func (g *guard) framesCleanLocked(t *frameEvidence) bool {
	if t == nil || t.Frame == nil || t.Frame.URL == nil || t.Frame.SecurityOrigin == nil || *t.Frame.URL == "" || *t.Frame.SecurityOrigin == "" {
		return false
	}
	if g.o.Loaded(*t.Frame.URL) || g.o.Loaded(*t.Frame.SecurityOrigin) || (t.Frame.UnreachableURL != nil && g.o.Loaded(*t.Frame.UnreachableURL)) {
		return false
	}
	for _, child := range t.ChildFrames {
		if !g.framesCleanLocked(child) {
			return false
		}
	}
	return true
}

func (g *guard) frameTreeCleanLocked(m message) bool {
	var r struct {
		FrameTree *frameEvidence `json:"frameTree"`
	}
	return m.Error == nil && json.Unmarshal(m.Result, &r) == nil && g.framesCleanLocked(r.FrameTree)
}

func (g *guard) lockFramesLocked(nt *navTarget) {
	g.logf("cdp: a target holds a protected frame, or wouldn't list its frames target=%s", nt.target)
	nt.historyLocked = true
}

func (g *guard) readFramesLocked(nt *navTarget, next func() [][]byte) [][]byte {
	if !nt.documentsOnly {
		return next()
	}
	return [][]byte{g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(m message) [][]byte {
		if !g.frameTreeCleanLocked(m) {
			g.lockFramesLocked(nt)
		}
		return next()
	})}
}

func (g *guard) commitFramesLocked(nt *navTarget, m *message) [][]byte {
	if m.Method != "Page.frameNavigated" || !nt.documentsOnly {
		return nil
	}
	return g.rereadFramesLocked(nt)
}

func (g *guard) framesGateLocked(id string) <-chan struct{} {
	s := g.sessions[id]
	if s == nil {
		return nil
	}
	nt := g.navTargets[s.target]
	if nt == nil || nt.framesGate == nil {
		return nil
	}
	return nt.framesGate
}

func (g *guard) plainErrorLocked(m *message, data []byte) []byte {
	if m.Error == nil || !g.documentsOnly {
		return data
	}
	var e struct {
		Code    json.Number `json:"code"`
		Message string      `json:"message"`
	}
	_ = json.Unmarshal(m.Error, &e)
	out := map[string]any{"id": *m.ID, "error": map[string]any{"code": e.Code, "message": e.Message}}
	if m.SessionID != "" {
		out["sessionId"] = m.SessionID
	}
	return encode(out)
}

func (g *guard) rereadFramesLocked(nt *navTarget) [][]byte {
	if nt.framesGate == nil {
		fresh := make(chan struct{})
		nt.framesGate = fresh
		time.AfterFunc(framesWait, func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if nt.framesGate == fresh {
				g.lockFramesLocked(nt)
				nt.openFramesGate()
			}
		})
	}
	gate := nt.framesGate
	nt.framesReads++
	return [][]byte{g.callbackLocked(nt.session, "Page.getFrameTree", nil, func(m message) [][]byte {
		if !g.frameTreeCleanLocked(m) {
			g.lockFramesLocked(nt)
		}
		if nt.framesGate != gate {
			return nil
		}
		if nt.framesReads--; nt.framesReads <= 0 {
			nt.openFramesGate()
		}
		return nil
	})}
}

func (nt *navTarget) openFramesGate() {
	if nt.framesGate != nil {
		close(nt.framesGate)
		nt.framesGate = nil
	}
	nt.framesReads = 0
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
	if !documentsPageCommands[c.method] || (c.method == "Fetch.enable" && !guardFetch(c)) || (c.method == "Network.setBypassServiceWorker" && c.params["bypass"] != true) {
		return refusedDocumentsCommand
	}
	target := g.sessions[c.session].target
	for _, selector := range documentsSelectors {
		if value, named := c.params[selector]; named && !g.mainContextLocked(target, selector, value) {
			return refusedDocumentsSelection
		}
	}
	return ""
}

func (g *guard) mainContextLocked(target, selector string, value any) bool {
	if selector == "frameId" {
		return value == target
	}
	shown := g.shown[target]
	if shown == nil {
		return false
	}
	for id, context := range shown.contexts {
		if context.frame != target {
			continue
		}
		switch selector {
		case "uniqueContextId":
			if value == context.unique && context.unique != "" {
				return true
			}
		case "contextId", "executionContextId":
			if n, ok := value.(json.Number); ok && n.String() == strconv.FormatInt(id, 10) {
				return true
			}
		}
	}
	return false
}
