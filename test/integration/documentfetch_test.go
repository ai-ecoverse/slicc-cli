package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

type documentBrowser struct {
	url string

	signal bool
	attest bool

	mu      sync.Mutex
	fetches []string
	methods map[string]bool
}

func (f *documentBrowser) saw(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[method]
}

func (f *documentBrowser) enabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetches...)
}

func newDocumentBrowser(t *testing.T, signal, attest bool) *documentBrowser {
	t.Helper()
	f := &documentBrowser{signal: signal, attest: attest, methods: map[string]bool{}}
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
	const url = "https://ordinary.test/"
	info := map[string]any{"targetId": "tab", "type": "page", "url": url, "title": "ordinary", "attached": false, "browserContextId": "default"}
	send := func(m map[string]any) bool {
		b, _ := json.Marshal(m)
		return c.Write(context.Background(), websocket.MessageText, b) == nil
	}
	sessions := 0
	guarded := ""
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
		f.methods[m.Method] = true
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
				result = map[string]any{"holdsDocuments": true}
				guarded = m.SessionID
			}
		case "Page.reload":
			paused := map[string]any{"requestId": "paused-1", "networkId": "paused-1", "frameId": "some-other-frame", "resourceType": "Document", "request": map[string]any{"url": "https://vault.sliccy.ai/"}}
			if !send(map[string]any{"method": "Fetch.requestPaused", "sessionId": guarded, "params": paused}) {
				return
			}
		case "Page.getNavigationHistory":
			result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 0, "url": url, "userTypedURL": url, "title": "ordinary", "transitionType": "link"}}}
		case "Page.getFrameTree":
			result = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "tab", "loaderId": "l", "url": url, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}
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
}

type documentResult struct {
	Value string `json:"value"`
	Error string `json:"error"`
}

func TestTheGuardFallsBackToDocumentsOnlyWhenTheBrowserSaysSo(t *testing.T) {
	const locked = "slicc can't keep seven's origin out of this target"
	const notAllowed = "allows only the commands measured not to reach one"
	const noSelector = "can't name a frame or an execution context"
	evaluate := documentStep{Method: "Runtime.evaluate", Params: map[string]any{"expression": "1", "returnByValue": true}}
	for _, c := range []struct {
		name    string
		signal  bool
		attest  bool
		steps   []documentStep
		want    []documentResult
		enables int
		reached []string
		unseen  []string
	}{
		{name: "a refusal without the signal gets no second try", steps: []documentStep{evaluate}, want: []documentResult{{Error: locked}}, enables: 1},
		{name: "an acknowledgment without the attestation stays refused", signal: true, steps: []documentStep{evaluate}, want: []documentResult{{Error: locked}}, enables: 2},
		{name: "a browser that says both is driven", signal: true, attest: true, steps: []documentStep{evaluate}, want: []documentResult{{Value: "answered"}}, enables: 2, reached: []string{"Runtime.evaluate"}},
		{
			name: "commands outside the allowlist are refused", signal: true, attest: true, enables: 2,
			steps:  []documentStep{{Method: "Page.captureScreenshot"}, {Method: "Page.printToPDF"}, {Method: "DOMSnapshot.captureSnapshot"}, {Method: "Page.createIsolatedWorld"}},
			want:   []documentResult{{Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}, {Error: notAllowed}},
			unseen: []string{"Page.captureScreenshot", "Page.printToPDF", "DOMSnapshot.captureSnapshot", "Page.createIsolatedWorld"},
		},
		{
			name: "a frame or context selector is refused on an allowed command", signal: true, attest: true, enables: 2,
			steps: []documentStep{
				{Method: "DOM.getDocument", Params: map[string]any{"pierce": true}},
				{Method: "Page.navigate", Params: map[string]any{"url": "https://ordinary.test/next", "frameId": "tab"}},
				{Method: "Runtime.callFunctionOn", Params: map[string]any{"functionDeclaration": "function(){}", "uniqueContextId": "x"}},
			},
			want:   []documentResult{{Error: noSelector}, {Error: noSelector}, {Error: noSelector}},
			unseen: []string{"DOM.getDocument", "Page.navigate", "Runtime.callFunctionOn"},
		},
		{
			name: "a session that isn't a page gets browser commands only", signal: true, attest: true, enables: 2,
			steps:  []documentStep{evaluate, {Method: "Page.captureScreenshot", BrowserScope: true}, {Method: "Runtime.getHeapUsage", BrowserScope: true}, {Method: "Browser.getVersion", BrowserScope: true}},
			want:   []documentResult{{Value: "answered"}, {Error: notAllowed}, {Error: notAllowed}, {Value: "sent"}},
			unseen: []string{"Page.captureScreenshot", "Runtime.getHeapUsage"},
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
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", fake.url, "--origin", srv.URL, "--name", "it-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var r struct {
				Results []documentResult `json:"results"`
				Error   string           `json:"error"`
			}
			env.p.eval("linkTest.cdp.steps("+jsArgs("documents", "tab", c.steps)+")", &r)
			if r.Error != "" || len(r.Results) != len(c.want) {
				t.Fatalf("%+v", r)
			}
			for i, want := range c.want {
				got := r.Results[i]
				if got.Value != want.Value || !strings.Contains(got.Error, want.Error) {
					t.Fatalf("step %d (%s): %+v, want %+v", i, c.steps[i].Method, got, want)
				}
			}
			enabled := fake.enabled()
			if len(enabled) != c.enables || strings.Contains(enabled[0], "resourceType") {
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
		})
	}
}
