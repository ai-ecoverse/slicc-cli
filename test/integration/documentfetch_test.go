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

	refuseAll bool

	mu      sync.Mutex
	fetches []string
}

func (f *documentBrowser) enabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetches...)
}

func newDocumentBrowser(t *testing.T, refuseAll bool) *documentBrowser {
	t.Helper()
	f := &documentBrowser{refuseAll: refuseAll}
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
			if f.refuseAll || !strings.Contains(string(m.Params), `"resourceType":"Document"`) {
				failure = map[string]any{"code": -32000, "message": "this browser pauses documents only"}
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

func TestTheGuardFallsBackToPausingDocuments(t *testing.T) {
	for _, c := range []struct {
		name      string
		refuseAll bool
		enables   int
		value     string
		refusal   string
	}{
		{name: "a browser that pauses only documents is guarded", enables: 2, value: "answered"},
		{name: "a browser that pauses nothing stays refused", refuseAll: true, enables: 2, refusal: "slicc can't keep seven's origin out of this target"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := launchBrowser(t)
			srv := pageServer(t)
			fake := newDocumentBrowser(t, c.refuseAll)
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", fake.url, "--origin", srv.URL, "--name", "it-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var r struct {
				Value string `json:"value"`
				Error string `json:"error"`
			}
			env.p.eval("linkTest.cdp.ask('documents', 'tab')", &r)
			if r.Value != c.value || !strings.Contains(r.Error, c.refusal) {
				t.Fatalf("%+v", r)
			}
			enabled := fake.enabled()
			if len(enabled) < c.enables || strings.Contains(enabled[0], "resourceType") || !strings.Contains(enabled[1], `"resourceType":"Document"`) {
				t.Fatalf("Fetch.enable calls: %q", enabled)
			}
		})
	}
}
