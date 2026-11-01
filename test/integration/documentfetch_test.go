package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type documentBrowser struct {
	url string

	signal     bool
	attest     bool
	delay      time.Duration
	earlyPause bool
	childFrame string
	firstTree  string
	nextTree   string

	mu      sync.Mutex
	fetches []string
	methods map[string]int
	worlds  []string
}

func (f *documentBrowser) saw(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[method] > 0
}

func (f *documentBrowser) worldParams() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.worlds...)
}

func (f *documentBrowser) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[method]
}

func (f *documentBrowser) enabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetches...)
}

const protectedAddress = "https://vault.sliccy.ai/callback?code=TOKEN-1"

func (f *documentBrowser) treeAnswer(restored bool) string {
	if restored {
		return f.nextTree
	}
	return f.firstTree
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func childFrames(child string) string {
	return `{"frameTree":{"frame":{"id":"tab","url":"https://ordinary.test/","securityOrigin":"https://ordinary.test"},"childFrames":[` + child + `]}}`
}

func newDocumentBrowser(t *testing.T, signal, attest bool) *documentBrowser {
	t.Helper()
	f := &documentBrowser{signal: signal, attest: attest, methods: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Documents/1.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/x"})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/devtools/browser/x", f.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *documentBrowser) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(-1)
	const tabURL = "https://ordinary.test/"
	info := map[string]any{"targetId": "tab", "type": "page", "url": tabURL, "title": "ordinary", "attached": false, "browserContextId": "default"}
	send := func(m map[string]any) bool {
		b, _ := json.Marshal(m)
		return c.Write(context.Background(), websocket.MessageText, b) == nil
	}
	sessions := 0
	guarded := ""
	restored := false
	pausedDocument := map[string]any{"requestId": "paused-1", "networkId": "paused-1", "frameId": "some-other-frame", "resourceType": "Document", "request": map[string]any{"url": "https://vault.sliccy.ai/"}}
	for {
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil {
			return
		}
		f.mu.Lock()
		f.methods[m.Method]++
		if m.Method == "Page.createIsolatedWorld" {
			f.worlds = append(f.worlds, string(m.Params))
		}
		f.mu.Unlock()
		result := map[string]any{}
		var failure map[string]any
		switch m.Method {
		case "Browser.getVersion":
			result = map[string]any{"product": "Documents/1.0", "protocolVersion": "1.3"}
		case "Target.attachToBrowserTarget":
			result = map[string]any{"sessionId": "browser"}
		case "Target.getTargets":
			result = map[string]any{"targetInfos": []any{info}}
		case "Target.getTargetInfo":
			result = map[string]any{"targetInfo": info}
		case "Target.attachToTarget":
			sessions++
			id := "page-" + strconv.Itoa(sessions)
			result = map[string]any{"sessionId": id}
			if !send(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": id, "targetInfo": info, "waitingForDebugger": false}}) {
				return
			}
		case "Fetch.enable":
			f.mu.Lock()
			f.fetches = append(f.fetches, string(m.Params))
			f.mu.Unlock()
			switch {
			case !strings.Contains(string(m.Params), `"resourceType":"Document"`):
				failure = map[string]any{"code": -32000, "message": "this browser pauses documents only"}
				if f.signal {
					failure["data"] = "documents-only"
				}
			case f.attest:
				if f.earlyPause && !send(map[string]any{"method": "Fetch.requestPaused", "sessionId": m.SessionID, "params": pausedDocument}) {
					return
				}
				time.Sleep(f.delay)
				result = map[string]any{"holdsDocuments": true}
				guarded = m.SessionID
			}
		case "Page.navigateToHistoryEntry":
			restored = true
			committed := map[string]any{"type": "Navigation", "frame": map[string]any{"id": "tab", "loaderId": "restored", "url": tabURL, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}
			if !send(map[string]any{"method": "Page.frameNavigated", "sessionId": guarded, "params": committed}) {
				return
			}
		case "DOM.getBoxModel":
			if !send(map[string]any{"method": "Fetch.requestPaused", "sessionId": guarded, "params": pausedDocument}) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			failure = map[string]any{"code": -32000, "message": "no box for " + protectedAddress, "data": map[string]any{"value": protectedAddress}}
		case "DOM.getContentQuads":
			frames := []map[string]any{{"url": protectedAddress, "functionName": "f", "scriptId": "1", "lineNumber": 0, "columnNumber": 0}}
			console := map[string]any{"type": "log", "args": []any{}, "executionContextId": 1, "timestamp": 1, "stackTrace": map[string]any{"callFrames": frames}}
			entry := map[string]any{"entry": map[string]any{"source": "network", "level": "error", "text": "failed", "timestamp": 1, "url": protectedAddress}}
			if !send(map[string]any{"method": "Runtime.consoleAPICalled", "sessionId": m.SessionID, "params": console}) || !send(map[string]any{"method": "Log.entryAdded", "sessionId": m.SessionID, "params": entry}) {
				return
			}
		case "Page.getLayoutMetrics":
			evented := map[string]any{"sessionId": "evented", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": "tab", "type": "browser", "url": "", "title": ""}}
			if !send(map[string]any{"method": "Target.attachedToTarget", "params": evented}) {
				return
			}
		case "Page.reload":
			if !send(map[string]any{"method": "Fetch.requestPaused", "sessionId": guarded, "params": pausedDocument}) {
				return
			}
		case "Page.getNavigationHistory":
			result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 0, "url": tabURL, "userTypedURL": tabURL, "title": "ordinary", "transitionType": "link"}}}
		case "Page.getFrameTree":
			if raw := f.treeAnswer(restored); raw != "" {
				if raw == "error" {
					failure = map[string]any{"code": -32000, "message": "no tree"}
					break
				}
				time.Sleep(f.delay)
				b, _ := json.Marshal(map[string]any{"id": m.ID, "sessionId": m.SessionID})
				if c.Write(context.Background(), websocket.MessageText, append(b[:len(b)-1], []byte(`,"result":`+raw+`}`)...)) != nil {
					return
				}
				continue
			}
			tree := map[string]any{"frame": map[string]any{"id": "tab", "loaderId": "l", "url": tabURL, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}
			if f.childFrame != "" {
				tree["childFrames"] = []any{map[string]any{"frame": map[string]any{"id": "tab.1", "parentId": "tab", "loaderId": "l", "url": f.childFrame, "securityOrigin": originOf(f.childFrame), "mimeType": "text/html"}}}
			}
			result = map[string]any{"frameTree": tree}
		case "Runtime.enable":
			for _, context := range []map[string]any{
				{"id": 1, "origin": "https://ordinary.test", "name": "", "uniqueId": "main", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": "tab"}},
				{"id": 2, "origin": "https://frame.test", "name": "", "uniqueId": "child", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": "tab.1"}},
			} {
				if !send(map[string]any{"method": "Runtime.executionContextCreated", "sessionId": m.SessionID, "params": map[string]any{"context": context}}) {
					return
				}
			}
		case "Runtime.evaluate":
			result = map[string]any{"result": map[string]any{"type": "string", "value": "answered"}}
		}
		out := map[string]any{"id": m.ID}
		if m.SessionID != "" {
			out["sessionId"] = m.SessionID
		}
		if failure != nil {
			out["error"] = failure
		} else {
			out["result"] = result
		}
		if !send(out) {
			return
		}
	}
}

type documentStep struct {
	Method       string         `json:"method"`
	Params       map[string]any `json:"params,omitempty"`
	BrowserScope bool           `json:"browserScope,omitempty"`
	Session      string         `json:"session,omitempty"`
}

type documentResult struct {
	Value string `json:"value"`
	Error string `json:"error"`
}

func TestTheGuardFallsBackToDocumentsOnlyWhenTheBrowserSaysSo(t *testing.T) {
	const locked = "slicc can't keep seven's origin out of this target"
	const notAllowed = "allows only the commands measured not to reach one"
	const noSelector = "can't name a frame or an execution context"
	const needsPage = "needs a page's session"
	const locksTab = "history may restore a protected document"
	evaluate := documentStep{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "returnByValue": true}}
	for _, c := range []struct {
		name      string
		signal    bool
		attest    bool
		delay     time.Duration
		early     bool
		noTab     bool
		child     string
		firstTree string
		nextTree  string
		hidden    string
		once      []string
		absent    string
		steps     []documentStep
		want      []documentResult
		enables   int
		reached   []string
		unseen    []string
	}{
		{name: "a refusal without the signal gets no second try", steps: []documentStep{evaluate}, want: []documentResult{{Error: locked}}, enables: 1},
		{name: "an acknowledgment without the attestation stays refused", signal: true, steps: []documentStep{evaluate}, want: []documentResult{{Error: locked}}, enables: 2},
		{name: "a browser that says both is driven", signal: true, attest: true, steps: []documentStep{evaluate}, want: []documentResult{{Value: "answered"}}, enables: 2, reached: []string{"Runtime.evaluate"}},
		{
			name: "commands outside the allowlist are refused", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "Page.captureScreenshot"}, {Method: "Page.printToPDF"}, {Method: "DOMSnapshot.captureSnapshot"}, {Method: "DOM.getOuterHTML"}},
			want:   []documentResult{{Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}},
			unseen: []string{"Page.captureScreenshot", "Page.printToPDF", "DOMSnapshot.captureSnapshot", "DOM.getOuterHTML"},
		},
		{
			name: "a frame or context selector is refused on an allowed command", signal: true, attest: true, enables: 2,
			steps: []documentStep{
				{Method: "DOM.getDocument", Params: map[string]any{"pierce": true}},
				{Method: "Page.navigate", Params: map[string]any{"url": "https://ordinary.test/next", "frameId": "tab.1"}},
				{Method: "Runtime.callFunctionOn", Params: map[string]any{"functionDeclaration": "function(){}", "uniqueContextId": "x"}},
			},
			want:   []documentResult{{Error: noSelector}, {Error: noSelector}, {Error: noSelector}},
			unseen: []string{"DOM.getDocument", "Page.navigate", "Runtime.callFunctionOn"},
		},
		{
			name: "a session that isn't a page gets browser commands only", signal: true, attest: true, enables: 2,
			steps:  []documentStep{evaluate, {Method: "Page.captureScreenshot", BrowserScope: true}, {Method: "Tracing.getCategories", BrowserScope: true}, {Method: "Browser.getVersion", BrowserScope: true}},
			want:   []documentResult{{Value: "answered"}, {Error: needsPage}, {Error: notAllowed}, {Value: "sent"}},
			unseen: []string{"Page.captureScreenshot", "Tracing.getCategories"},
		},
		{
			name: "a content command needs a page session before any attach", signal: true, attest: true, noTab: true, enables: 0,
			steps:  []documentStep{{Method: "Page.captureScreenshot", BrowserScope: true}, {Method: "Runtime.evaluate", Params: map[string]any{"expression": "1"}, BrowserScope: true}},
			want:   []documentResult{{Error: needsPage}, {Error: needsPage}},
			unseen: []string{"Page.captureScreenshot", "Runtime.evaluate"},
		},
		{
			name: "a content command needs a page session during attestation", signal: true, attest: true, delay: 1500 * time.Millisecond, enables: 2,
			steps:  []documentStep{{Method: "Page.captureScreenshot", BrowserScope: true}, {Method: "Runtime.evaluate", Params: map[string]any{"expression": "1"}, BrowserScope: true}},
			want:   []documentResult{{Error: needsPage}, {Error: needsPage}},
			unseen: []string{"Page.captureScreenshot", "Runtime.evaluate"},
		},
		{
			name: "an attachment reported as a browser's gets no page commands", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "Page.getLayoutMetrics"}, {Method: "Security.enable", Session: "evented"}},
			want:   []documentResult{{Value: "sent"}, {Error: notAllowed}},
			unseen: []string{"Security.enable"},
		},
		{
			name: "a client's guard is answered without touching the hold", signal: true, attest: true, enables: 2,
			steps: []documentStep{
				{Method: "Fetch.enable", Params: map[string]any{"patterns": []any{map[string]any{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}}}},
				{Method: "Network.setBypassServiceWorker", Params: map[string]any{"bypass": true}},
				{Method: "Fetch.disable"},
				{Method: "Page.reload"},
				evaluate,
			},
			want:    []documentResult{{Value: "sent"}, {Value: "sent"}, {Value: "sent"}, {Value: "sent"}, {Error: "history may restore a protected document"}},
			reached: []string{"Fetch.failRequest"},
			unseen:  []string{"Fetch.disable"},
			once:    []string{"Network.setBypassServiceWorker"},
		},
		{
			name: "a client can't reconfigure a tab-wide hold", signal: true, attest: true, enables: 2,
			steps: []documentStep{
				{Method: "Fetch.enable", Params: map[string]any{"patterns": []any{map[string]any{"urlPattern": "https://ordinary.test/*", "resourceType": "Document"}}}},
				{Method: "Fetch.continueRequest", Params: map[string]any{"requestId": "x"}},
				{Method: "Network.disable"},
				{Method: "Network.setBypassServiceWorker", Params: map[string]any{"bypass": false}},
			},
			want:   []documentResult{{Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}},
			unseen: []string{"Network.disable", "Fetch.continueRequest"},
			once:   []string{"Network.setBypassServiceWorker"},
		},
		{
			name: "a tab that lists a protected frame locks, and its address is cut", signal: true, attest: true, enables: 2, child: protectedAddress,
			steps:  []documentStep{evaluate, {Method: "Page.getFrameTree"}},
			want:   []documentResult{{Error: locksTab}, {Value: "sent"}},
			hidden: "TOKEN-1",
			unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a tree without a frame locks", signal: true, attest: true, enables: 2, firstTree: `{"frameTree":{}}`,
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a null child locks", signal: true, attest: true, enables: 2, firstTree: childFrames(`null`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a child without an origin locks", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"https://frame.test/"}}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a protected URL under an ordinary origin locks", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"https://vault.sliccy.ai/x","securityOrigin":"https://frame.test"}}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a blank child on a protected origin locks", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"about:blank","securityOrigin":"https://vault.sliccy.ai"}}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "a child with a protected unreachable URL locks", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"about:blank","securityOrigin":"null","unreachableUrl":"https://vault.sliccy.ai/x"}}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "evidence nested below an ordinary child counts", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"https://frame.test/","securityOrigin":"https://frame.test"},"childFrames":[{"frame":{"id":"d","url":"about:blank","securityOrigin":"https://vault.sliccy.ai"}}]}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Error: locksTab}}, unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "an opaque blank child is clean", signal: true, attest: true, enables: 2, firstTree: childFrames(`{"frame":{"id":"c","url":"about:blank","securityOrigin":"null"}}`),
			steps: []documentStep{evaluate}, want: []documentResult{{Value: "answered"}},
		},
		{
			name: "a restore that brings a protected frame back locks, and nothing runs before the answer", signal: true, attest: true, enables: 2, delay: 300 * time.Millisecond,
			nextTree: childFrames(`{"frame":{"id":"c","url":"` + protectedAddress + `","securityOrigin":"https://vault.sliccy.ai"}}`),
			steps:    []documentStep{{Method: "Page.navigateToHistoryEntry", Params: map[string]any{"entryId": 0}}, evaluate},
			want:     []documentResult{{Value: "sent"}, {Error: locksTab}},
			hidden:   "TOKEN-1",
			unseen:   []string{"Runtime.evaluate"},
		},
		{
			name: "a restore with no tree locks", signal: true, attest: true, enables: 2, nextTree: "error",
			steps:  []documentStep{{Method: "Page.navigateToHistoryEntry", Params: map[string]any{"entryId": 0}}, evaluate},
			want:   []documentResult{{Value: "sent"}, {Error: locksTab}},
			unseen: []string{"Runtime.evaluate"},
		},
		{
			name: "universal access never reaches the browser", signal: true, attest: true, enables: 2,
			steps:   []documentStep{{Method: "Page.createIsolatedWorld", Params: map[string]any{"frameId": "tab", "grantUniveralAccess": true}}},
			want:    []documentResult{{Value: "sent"}},
			reached: []string{"Page.createIsolatedWorld"},
			absent:  "grantUniveralAccess",
		},
		{
			name: "an error that outlives a lock doesn't get through", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "DOM.getBoxModel", Params: map[string]any{"nodeId": 1}}},
			want:   []documentResult{{Error: "while this command ran"}},
			hidden: "TOKEN-1",
		},
		{
			name: "diagnostic events don't carry the address", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "DOM.getContentQuads", Params: map[string]any{"nodeId": 1}}},
			want:   []documentResult{{Value: "sent"}},
			hidden: "TOKEN-1",
		},
		{
			name: "an ordinary frame doesn't lock the tab", signal: true, attest: true, enables: 2, child: "https://frame.test/",
			steps: []documentStep{evaluate},
			want:  []documentResult{{Value: "answered"}},
		},
		{
			name: "Slicc.action never reaches the browser, and its client's input doesn't either", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "Slicc.action", Params: map[string]any{"phase": "begin"}, BrowserScope: true}, {Method: "Input.dispatchMouseEvent", Params: map[string]any{"type": "mouseMoved", "x": 1, "y": 1}}},
			want:   []documentResult{{Value: "sent"}, {Value: "sent"}},
			unseen: []string{"Slicc.action", "Input.dispatchMouseEvent"},
		},
		{
			name: "input from a client without Slicc.action reaches the browser", signal: true, attest: true, enables: 2,
			steps:   []documentStep{{Method: "Input.dispatchMouseEvent", Params: map[string]any{"type": "mouseMoved", "x": 1, "y": 1}}},
			want:    []documentResult{{Value: "sent"}},
			reached: []string{"Input.dispatchMouseEvent"},
		},
		{
			name: "only the main frame's contexts can be named", signal: true, attest: true, enables: 2,
			steps: []documentStep{
				{Method: "Runtime.enable"},
				{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "contextId": 1}},
				{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "uniqueContextId": "main"}},
				{Method: "Page.createIsolatedWorld", Params: map[string]any{"frameId": "tab"}},
				{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "contextId": 2}},
				{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "uniqueContextId": "child"}},
				{Method: "Page.createIsolatedWorld", Params: map[string]any{"frameId": "tab.1"}},
			},
			want: []documentResult{{Value: "sent"}, {Value: "answered"}, {Value: "answered"}, {Value: "sent"}, {Error: noSelector}, {Error: noSelector}, {Error: noSelector}},
		},
		{
			name: "a pause during attestation locks the tab too", signal: true, attest: true, early: true, enables: 2,
			steps:   []documentStep{evaluate},
			want:    []documentResult{{Error: "history may restore a protected document"}},
			reached: []string{"Fetch.failRequest"},
		},
		{
			name: "a paused protected document in any frame is failed and locks the tab", signal: true, attest: true, enables: 2,
			steps:   []documentStep{{Method: "Page.reload"}, evaluate},
			want:    []documentResult{{Value: "sent"}, {Error: "history may restore a protected document"}},
			reached: []string{"Fetch.failRequest"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := launchBrowser(t)
			srv := pageServer(t)
			fake := newDocumentBrowser(t, c.signal, c.attest)
			fake.delay, fake.earlyPause, fake.childFrame, fake.firstTree, fake.nextTree = c.delay, c.early, c.child, c.firstTree, c.nextTree
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", fake.url, "--origin", srv.URL, "--name", "it-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var r struct {
				Results    []documentResult `json:"results"`
				Transcript string           `json:"transcript"`
				Error      string           `json:"error"`
			}
			tab := "tab"
			if c.noTab {
				tab = ""
			}
			env.p.eval("linkTest.cdp.steps("+jsArgs("documents", tab, c.steps)+")", &r)
			if r.Error != "" || len(r.Results) != len(c.want) {
				t.Fatalf("%+v", r)
			}
			for i, want := range c.want {
				got := r.Results[i]
				if got.Value != want.Value || !strings.Contains(got.Error, want.Error) {
					t.Fatalf("step %d (%s): %+v, want %+v", i, c.steps[i].Method, got, want)
				}
			}
			if c.hidden != "" && strings.Contains(r.Transcript, c.hidden) {
				t.Fatalf("the stream carried %s: %s", c.hidden, r.Transcript)
			}
			enabled := fake.enabled()
			if len(enabled) != c.enables || c.enables > 0 && strings.Contains(enabled[0], "resourceType") {
				t.Fatalf("Fetch.enable calls: %q", enabled)
			}
			if c.enables > 1 && !strings.Contains(enabled[1], `"resourceType":"Document"`) {
				t.Fatalf("Fetch.enable calls: %q", enabled)
			}
			for _, method := range c.reached {
				if !fake.saw(method) {
					t.Fatalf("the browser never got %s", method)
				}
			}
			for _, method := range c.unseen {
				if fake.saw(method) {
					t.Fatalf("the browser got %s", method)
				}
			}
			if c.absent != "" && strings.Contains(strings.Join(fake.worldParams(), ""), c.absent) {
				t.Fatalf("the browser got %s: %v", c.absent, fake.worldParams())
			}
			for _, method := range c.once {
				if n := fake.count(method); n != 1 {
					t.Fatalf("the browser got %s %d times, and only the guard sends it", method, n)
				}
			}
		})
	}
}
