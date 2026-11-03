package stage1

import (
	"encoding/json"
	"fmt"
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
	Server        *httptest.Server
	Closed        chan struct{}
	closed        sync.Once
	Resumes       chan string
	FrameReads    chan struct{}
	mu            sync.Mutex
	write         sync.Mutex
	conn          *websocket.Conn
	trace         []Packet
	url           string
	loader        string
	convert       bool
	activeScript  string
	activeContext int
	Released      bool
	HoldReads     bool
	childURL      *string
	commitMethod  string
	revokeReply   bool
}

func NewBrowser(t *testing.T) *Browser {
	t.Helper()
	b := &Browser{Closed: make(chan struct{}), Resumes: make(chan string, 64), FrameReads: make(chan struct{}, 64), activeScript: "script", activeContext: 7, url: "https://ordinary.test/doc", loader: "generation"}
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
		defer func() { _ = conn.CloseNow(); b.closedConnection() }()
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
			for _, event := range b.beforeReply(p.Method) {
				data, _ = json.Marshal(event)
				if err = conn.Write(r.Context(), websocket.MessageText, data); err != nil {
					break
				}
			}
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

func (b *Browser) closedConnection() {
	b.closed.Do(func() { close(b.Closed) })
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
		select {
		case b.FrameReads <- struct{}{}:
		default:
		}
		if b.HoldReads {
			return result, nil, true
		}
		tree := map[string]any{"frame": map[string]any{"id": "T", "url": b.url, "securityOrigin": "https://ordinary.test", "loaderId": b.loader}}
		if b.childURL != nil {
			origin := strings.TrimSuffix(*b.childURL, "/doc")
			if origin == "" {
				origin = "://"
			}
			tree["childFrames"] = []any{map[string]any{"frame": map[string]any{"id": "C", "parentId": "T", "url": *b.childURL, "securityOrigin": origin, "loaderId": "child-generation"}}}
		}
		result = map[string]any{"frameTree": tree}
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
	return map[string]any{"method": "Debugger.paused", "sessionId": "S", "params": map[string]any{"reason": reason, "callFrames": []any{map[string]any{"callFrameId": fmt.Sprintf(`{"injectedScriptId":%d,"id":0}`, b.activeContext), "location": map[string]any{"scriptId": b.activeScript, "lineNumber": 0, "columnNumber": 0}, "url": "https://ordinary.test/first.js", "scopeChain": []any{map[string]any{"type": "global", "object": map[string]any{"objectId": fmt.Sprintf("1.%d.1", b.activeContext)}}}}}}}
}

func (b *Browser) Emit(t *testing.T, method string) {
	t.Helper()
	b.mu.Lock()
	var event map[string]any
	var before []map[string]any
	switch method {
	case "evaluate":
		b.activeScript = "evaluate"
		before = append(before, map[string]any{"method": "Debugger.scriptParsed", "sessionId": "S", "params": map[string]any{"scriptId": "evaluate", "executionContextId": 7, "url": "", "embedderName": ""}})
		event = b.pause("instrumentation")
	case "next-context":
		b.activeScript, b.activeContext = "next-script", 8
		before = append(before, map[string]any{"method": "Runtime.executionContextCreated", "sessionId": "S", "params": map[string]any{"context": map[string]any{"id": 8, "origin": "https://ordinary.test", "uniqueId": "next-context", "auxData": map[string]any{"isDefault": true, "frameId": "T"}}}}, map[string]any{"method": "Debugger.scriptParsed", "sessionId": "S", "params": map[string]any{"scriptId": "next-script", "executionContextId": 8, "url": "https://ordinary.test/next.js", "embedderName": "https://ordinary.test/next.js"}})
		event = b.pause("instrumentation")
	case "instrumentation":
		event = b.pause(method)
	case "forwardable", "small-forwardable":
		params := map[string]any{"timestamp": 1}
		if method == "forwardable" {
			params["padding"] = strings.Repeat("x", 32<<10)
		}
		event = map[string]any{"method": "Page.loadEventFired", "sessionId": "S", "params": params}
	case "normal":
		event = b.pause("other")
	case "async", "parent-stack", "unplaced-async":
		context := 7
		if method == "unplaced-async" {
			context = 77
		}
		before = append(before, map[string]any{"method": "Debugger.scriptParsed", "sessionId": "S", "params": map[string]any{"scriptId": "async-script", "executionContextId": context, "url": "https://ordinary.test/async.js"}})
		event = b.pause("other")
		stack := map[string]any{"callFrames": []any{map[string]any{"scriptId": "async-script", "url": "https://ordinary.test/async.js", "lineNumber": 0, "columnNumber": 0}}}
		if method == "parent-stack" {
			stack = map[string]any{"callFrames": []any{}, "parent": stack}
		}
		event["params"].(map[string]any)["asyncStackTrace"] = stack
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
	for _, packet := range append(before, event) {
		data, _ := json.Marshal(packet)
		if err := conn.Write(t.Context(), websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
	}
}

func (b *Browser) Script(t *testing.T, id string, size int) {
	t.Helper()
	b.mu.Lock()
	b.activeScript, b.activeContext = id, 7
	conn := b.conn
	b.mu.Unlock()
	packet := map[string]any{"method": "Debugger.scriptParsed", "sessionId": "S", "params": map[string]any{"scriptId": id, "executionContextId": 7, "url": "https://ordinary.test/" + strings.Repeat("x", size)}}
	data, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	b.write.Lock()
	defer b.write.Unlock()
	if err := conn.Write(t.Context(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func (b *Browser) Trace() []Packet {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Packet(nil), b.trace...)
}

func (b *Browser) SetChild(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.childURL = &url
}

func (b *Browser) StallReads() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.HoldReads = true
}

func (b *Browser) CommitOnReply(method string, revoked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commitMethod, b.revokeReply = method, revoked
}

func (b *Browser) beforeReply(method string) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.commitMethod != method {
		return nil
	}
	b.commitMethod = ""
	b.loader = "reply-generation"
	events := []map[string]any{b.frame("N")}
	if b.revokeReply {
		events = append(events, map[string]any{"method": "Runtime.executionContextCreated", "sessionId": "N", "params": map[string]any{"context": map[string]any{"id": 9, "origin": "chrome-extension://foreign", "uniqueId": "foreign", "auxData": map[string]any{"isDefault": false, "frameId": "T"}}}})
	}
	return events
}

func (b *Browser) NextFrameRead() <-chan struct{} {
	for {
		select {
		case <-b.FrameReads:
		default:
			return b.FrameReads
		}
	}
}
