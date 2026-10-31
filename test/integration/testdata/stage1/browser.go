package stage1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

type Packet struct {
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Session string         `json:"sessionId"`
	Params  map[string]any `json:"params"`
}

type Browser struct {
	Server    *httptest.Server
	Closed    chan struct{}
	Resumes   chan string
	mu        sync.Mutex
	write     sync.Mutex
	conn      *websocket.Conn
	trace     []Packet
	url       string
	loader    string
	convert   bool
	Released  bool
	HoldReads bool
}

func NewBrowser(t *testing.T) *Browser {
	t.Helper()
	b := &Browser{Closed: make(chan struct{}), Resumes: make(chan string, 64), url: "https://ordinary.test/doc", loader: "generation"}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Chrome/156.0.8078.80", "Protocol-Version": "1.3", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/stage1"})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) })
	mux.HandleFunc("/devtools/browser/stage1", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		b.mu.Lock()
		b.conn = conn
		b.mu.Unlock()
		defer func() { _ = conn.CloseNow(); close(b.Closed) }()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var p Packet
			if json.Unmarshal(data, &p) != nil {
				return
			}
			result, events, hold := b.answer(p)
			if hold {
				continue
			}
			reply := map[string]any{"id": p.ID, "result": result}
			if p.Session != "" {
				reply["sessionId"] = p.Session
			}
			b.write.Lock()
			data, _ = json.Marshal(reply)
			err = conn.Write(r.Context(), websocket.MessageText, data)
			for _, event := range events {
				if err != nil {
					break
				}
				data, _ = json.Marshal(event)
				err = conn.Write(r.Context(), websocket.MessageText, data)
			}
			b.write.Unlock()
			if err != nil {
				return
			}
		}
	})
	b.Server = httptest.NewServer(mux)
	t.Cleanup(b.Server.Close)
	return b
}

func (b *Browser) frame(session string) map[string]any {
	return map[string]any{"method": "Page.frameNavigated", "sessionId": session, "params": map[string]any{"frame": map[string]any{"id": "T", "url": b.url, "securityOrigin": strings.TrimSuffix(b.url, "/doc"), "loaderId": b.loader}}}
}

func (b *Browser) answer(p Packet) (any, []map[string]any, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trace = append(b.trace, p)
	result := any(map[string]any{})
	var events []map[string]any
	switch p.Method {
	case "Target.attachToBrowserTarget":
		result = map[string]any{"sessionId": "R"}
	case "Target.getTargets":
		result = map[string]any{"targetInfos": []any{}}
	case "Target.createTarget":
		result = map[string]any{"targetId": "T"}
		events = append(events, map[string]any{"method": "Target.targetCreated", "sessionId": "R", "params": map[string]any{"targetInfo": map[string]any{"targetId": "T", "type": "page", "url": b.url}}})
	case "Target.attachToTarget":
		session := "S"
		if p.ID >= 1879048192 {
			session = "N"
		}
		result = map[string]any{"sessionId": session}
	case "Target.getTargetInfo":
		result = map[string]any{"targetInfo": map[string]any{"targetId": "T", "type": "page", "url": b.url}}
	case "Page.getNavigationHistory":
		result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": b.url}}}
	case "Page.enable":
		events = append(events, b.frame(p.Session))
	case "Runtime.enable":
		events = append(events, map[string]any{"method": "Runtime.executionContextCreated", "sessionId": p.Session, "params": map[string]any{"context": map[string]any{"id": 7, "origin": "https://ordinary.test", "uniqueId": "context", "auxData": map[string]any{"isDefault": true, "frameId": "T"}}}})
	case "Page.getFrameTree":
		if b.HoldReads {
			return result, nil, true
		}
		result = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "T", "url": b.url, "securityOrigin": "https://ordinary.test", "loaderId": b.loader}}}
	case "Debugger.enable":
		events = append(events, map[string]any{"method": "Debugger.scriptParsed", "sessionId": p.Session, "params": map[string]any{"scriptId": "script", "executionContextId": 7, "url": "https://ordinary.test/first.js"}})
	case "Debugger.setInstrumentationBreakpoint":
		result = map[string]any{"breakpointId": "B"}
	case "Debugger.pause":
		b.convert = true
	case "Debugger.resume":
		if b.convert {
			b.convert = false
			events = append(events, b.pause("other"))
		} else {
			b.Released = true
			b.Resumes <- p.Session
		}
	case "Debugger.getScriptSource":
		result = map[string]any{"scriptSource": "globalThis.stage1Effect = 1"}
	case "Debugger.evaluateOnCallFrame":
		result = map[string]any{"result": map[string]any{"value": 42}}
	}
	return result, events, false
}

func (b *Browser) pause(reason string) map[string]any {
	return map[string]any{"method": "Debugger.paused", "sessionId": "S", "params": map[string]any{"reason": reason, "callFrames": []any{map[string]any{"callFrameId": `{"injectedScriptId":7,"id":0}`, "location": map[string]any{"scriptId": "script", "lineNumber": 0, "columnNumber": 0}, "url": "https://ordinary.test/first.js", "scopeChain": []any{map[string]any{"type": "global", "object": map[string]any{"objectId": "1.7.1"}}}}}}}
}

func (b *Browser) Emit(t *testing.T, method string) {
	t.Helper()
	b.mu.Lock()
	var event map[string]any
	switch method {
	case "instrumentation":
		event = b.pause(method)
	case "normal":
		event = b.pause("other")
	case "commit":
		b.loader = "next-generation"
		event = b.frame("N")
	case "protected":
		b.url = "https://seven.sliccy.ai/doc"
		b.loader = "protected-generation"
		event = b.frame("N")
	case "foreign":
		event = map[string]any{"method": "Runtime.executionContextCreated", "sessionId": "N", "params": map[string]any{"context": map[string]any{"id": 9, "origin": "chrome-extension://foreign", "uniqueId": "foreign", "auxData": map[string]any{"isDefault": false, "frameId": "T"}}}}
	}
	conn := b.conn
	b.mu.Unlock()
	b.write.Lock()
	defer b.write.Unlock()
	data, _ := json.Marshal(event)
	if err := conn.Write(t.Context(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func (b *Browser) Trace() []Packet {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Packet(nil), b.trace...)
}

func (b *Browser) StallReads() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.HoldReads = true
}
