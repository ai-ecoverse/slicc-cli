package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeScript func(method string, params json.RawMessage) (result string, after []string)

type parkedReply struct {
	reply   string
	after   []string
	tag     string
	session string
}

type overlapSession struct {
	shownDone      bool
	admitDirtyDone bool
	awaitDirty     bool
	parkNext       bool
}

type fakeCDPBrowser struct {
	srv         *httptest.Server
	script      fakeScript
	mu          sync.Mutex
	got         []string
	slow        map[string]time.Duration
	pulse       []string
	gates       map[string]string
	parked      []parkedReply
	captures    int
	overlapHold bool
	holdParked  bool
	overlap     map[string]*overlapSession
}

func (f *fakeCDPBrowser) gate(method, until string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gates == nil {
		f.gates = map[string]string{}
	}
	f.gates[method] = until
}

func (f *fakeCDPBrowser) every(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulse = append(f.pulse, event)
}

func (f *fakeCDPBrowser) delay(method string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slow == nil {
		f.slow = map[string]time.Duration{}
	}
	f.slow[method] = d
}

func (f *fakeCDPBrowser) overlapCaptureHolds() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overlapHold = true
	if f.overlap == nil {
		f.overlap = map[string]*overlapSession{}
	}
}

func (f *fakeCDPBrowser) takeParked(tag, session string) []parkedReply {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tag == "" {
		out := f.parked
		f.parked = nil
		return out
	}
	var keep, out []parkedReply
	for _, p := range f.parked {
		if p.tag == tag && (session == "" || p.session == session) {
			out = append(out, p)
		} else {
			keep = append(keep, p)
		}
	}
	f.parked = keep
	return out
}

func takeAhead(result string) ([]string, string) {
	const prefix = "#ahead:"
	if !strings.HasPrefix(result, prefix) {
		return nil, result
	}
	body := result[len(prefix):]
	bar := strings.IndexByte(body, '|')
	if bar < 0 {
		return nil, result
	}
	var ahead []string
	if json.Unmarshal([]byte(body[:bar]), &ahead) != nil {
		return nil, result
	}
	return ahead, body[bar+1:]
}

func writeCall(ctx context.Context, c *websocket.Conn, ahead []string, reply string, after []string) bool {
	for _, e := range ahead {
		if c.Write(ctx, websocket.MessageText, []byte(e)) != nil {
			return false
		}
	}
	if c.Write(ctx, websocket.MessageText, []byte(reply)) != nil {
		return false
	}
	for _, e := range after {
		if c.Write(ctx, websocket.MessageText, []byte(e)) != nil {
			return false
		}
	}
	return true
}

func writeParked(ctx context.Context, c *websocket.Conn, parked []parkedReply) bool {
	for _, p := range parked {
		if c.Write(ctx, websocket.MessageText, []byte(p.reply)) != nil {
			return false
		}
		for _, e := range p.after {
			if c.Write(ctx, websocket.MessageText, []byte(e)) != nil {
				return false
			}
		}
	}
	return true
}

func afterEnable(events []string) fakeScript {
	return func(method string, _ json.RawMessage) (string, []string) {
		if method == "Network.enable" {
			return "{}", events
		}
		return "{}", nil
	}
}

func (f *fakeCDPBrowser) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func newFakeCDPBrowser(t *testing.T, script fakeScript) *fakeCDPBrowser {
	t.Helper()
	f := &fakeCDPBrowser{script: script}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/154.0.8037.98",
			"Protocol-Version":     "1.3",
			"User-Agent":           "Mozilla/5.0 Fake Chrome/154.0.8037.98 Safari/537.36",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/fake",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	mux.HandleFunc("/devtools/browser/fake", f.serve)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCDPBrowser) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(-1)
	ctx := r.Context()
	f.mu.Lock()
	pulse := append([]string(nil), f.pulse...)
	f.mu.Unlock()
	held := map[string][]string{}
	seen := map[string]bool{}
	if len(pulse) > 0 {
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
				for _, e := range pulse {
					if c.Write(ctx, websocket.MessageText, []byte(e)) != nil {
						return
					}
				}
			}
		}()
	}
	for {
		_, data, err := c.Read(ctx)
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
		f.got = append(f.got, string(data))
		f.mu.Unlock()
		var result string
		var after []string
		var ahead []string
		switch m.Method {
		case "Target.attachToBrowserTarget":
			result = `{"sessionId":"RELAY"}`
			if m.ID < 0x70000000 {
				result = `{"sessionId":"CLIENT_BROWSER"}`
			}
		case "Target.getTargets":
			result, after = f.script(m.Method, m.Params)
			ahead, result = takeAhead(result)
			if result == "{}" {
				result = `{"targetInfos":[]}`
			}
		default:
			result, after = f.script(m.Method, m.Params)
			ahead, result = takeAhead(result)
			if result == "{}" && m.Method == "Page.getNavigationHistory" {
				result = `{"currentIndex":0,"entries":[{"id":1,"url":"about:blank"}]}`
			}
			if result == "{}" && m.Method == "Target.getTargetInfo" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(m.Params, &params)
				result = fmt.Sprintf(`{"targetInfo":{"targetId":%q,"type":"page","url":"about:blank"}}`, params.TargetID)
			}
		}
		pendingTag := ""
		if rest, ok := strings.CutPrefix(result, "#pending"); ok {
			pendingTag = "hold"
			if strings.HasPrefix(rest, "|") {
				result = rest[1:]
			} else if strings.HasPrefix(rest, ":") {
				pendingTag = rest[1:]
				if i := strings.IndexByte(pendingTag, '|'); i >= 0 {
					result = pendingTag[i+1:]
					pendingTag = pendingTag[:i]
				} else {
					result = "{}"
				}
			} else {
				result = "{}"
			}
		}
		flushHolds := false
		flushDirty := false
		f.mu.Lock()
		if pendingTag == "" && f.overlapHold && m.SessionID != "" {
			st := f.overlap[m.SessionID]
			if st == nil {
				st = &overlapSession{}
				f.overlap[m.SessionID] = st
			}
			switch m.Method {
			case "Page.captureScreenshot":
				if f.captures == 0 {
					if st.admitDirtyDone {
						f.captures++
						st.parkNext = true
					} else {
						st.awaitDirty = true
						pendingTag = "await-dirty"
					}
				} else {
					f.captures++
				}
			case "Page.getLayoutMetrics":
				if !f.holdParked {
					pendingTag = "await-hold"
				} else if f.captures >= 2 {
					flushHolds = true
				}
			case "DOM.getDocument", "Page.createIsolatedWorld", "DOM.getFrameOwner", "DOM.resolveNode", "Runtime.callFunctionOn":
				if st.awaitDirty && m.Method == "Runtime.callFunctionOn" {
					st.awaitDirty = false
					st.admitDirtyDone = true
					st.parkNext = true
					f.captures++
					flushDirty = true
				} else if st.parkNext && f.captures >= 1 {
					st.parkNext = false
					pendingTag = "hold"
				} else if m.Method == "Runtime.callFunctionOn" {
					if st.shownDone {
						st.admitDirtyDone = true
					} else {
						st.shownDone = true
					}
				}
			}
		}
		f.mu.Unlock()
		field := `"result":` + result
		if msg, failed := strings.CutPrefix(result, "!"); failed {
			field = fmt.Sprintf(`"error":{"code":-32000,"message":%q}`, msg)
		}
		reply := fmt.Sprintf(`{"id":%d,%s}`, m.ID, field)
		if m.SessionID != "" {
			reply = fmt.Sprintf(`{"id":%d,"sessionId":%q,%s}`, m.ID, m.SessionID, field)
		}
		if flushDirty {
			if !writeParked(ctx, c, f.takeParked("await-dirty", m.SessionID)) {
				return
			}
			if !writeCall(ctx, c, ahead, reply, after) {
				return
			}
			continue
		}
		if pendingTag != "" {
			f.mu.Lock()
			f.parked = append(f.parked, parkedReply{reply: reply, after: after, tag: pendingTag, session: m.SessionID})
			parkedHold := pendingTag == "hold"
			if parkedHold {
				f.holdParked = true
			}
			f.mu.Unlock()
			if parkedHold {
				if !writeParked(ctx, c, f.takeParked("await-hold", "")) {
					return
				}
			}
			continue
		}
		seen[m.Method] = true
		f.mu.Lock()
		d := f.slow[m.Method]
		until := f.gates[m.Method]
		f.mu.Unlock()
		if until != "" && !seen[until] {
			held[until] = append(held[until], reply)
			continue
		}
		if d > 0 {
			go func() {
				time.Sleep(d)
				_ = writeCall(ctx, c, ahead, reply, after)
			}()
			continue
		}
		if !writeCall(ctx, c, ahead, reply, append(after, held[m.Method]...)) {
			return
		}
		delete(held, m.Method)
		if flushHolds {
			if !writeParked(ctx, c, f.takeParked("hold", "")) {
				return
			}
		}
	}
}

type extraEvent struct {
	Method    string `json:"method"`
	RequestID string `json:"requestId"`
	Text      string `json:"text"`
}

func netEvent(method, params string) string {
	return `{"method":"Network.` + method + `","params":` + params + `}`
}

func extraInfoScript(seven, allowed string) []string {
	req := func(id, url, extra string) string {
		return netEvent("requestWillBeSent", `{"requestId":"`+id+`","request":{"url":"`+url+`","headers":{}}`+extra+`}`)
	}
	res := func(id, url string) string {
		return netEvent("responseReceived", `{"requestId":"`+id+`","response":{"url":"`+url+`","headers":{}}}`)
	}
	reqExtra := func(id, cookie string) string {
		return netEvent("requestWillBeSentExtraInfo", `{"requestId":"`+id+`","associatedCookies":[],"headers":{"Cookie":"`+cookie+`"}}`)
	}
	resExtra := func(id, cookie string) string {
		return netEvent("responseReceivedExtraInfo", `{"requestId":"`+id+`","statusCode":200,"headers":{"Set-Cookie":"`+cookie+`"},"headersText":"HTTP/1.1 200 OK\r\nSet-Cookie: `+cookie+`\r\n\r\n"}`)
	}
	done := func(id string) string {
		return netEvent("loadingFinished", `{"requestId":"`+id+`","timestamp":1,"encodedDataLength":1}`)
	}
	return []string{
		resExtra("early", "s=secret-early"), reqExtra("early", "s=secret-early"),
		req("early", seven+"/sse", ""), res("early", seven+"/sse"), done("early"),
		reqExtra("plain", "a=kept-cookie"), resExtra("plain", "a=kept-set"),
		req("plain", allowed+"/sse", ""), res("plain", allowed+"/sse"), done("plain"),
		req("hop", allowed+"/go", ""), reqExtra("hop", "a=kept-hop"), resExtra("hop", "s=secret-hop"),
		req("hop", seven+"/sse", `,"redirectResponse":{"url":"`+allowed+`/go","headers":{}}`), res("hop", seven+"/sse"),
		resExtra("orphan", "s=secret-orphan"),
	}
}

func setupFakeProtect(t *testing.T, script func(seven, allowed string) fakeScript) (*protectEnv, *fakeCDPBrowser) {
	t.Helper()
	return setupFakeProtectEnv(t, nil, script)
}

func setupFakeProtectEnv(t *testing.T, attachEnv []string, script func(seven, allowed string) fakeScript) (*protectEnv, *fakeCDPBrowser) {
	t.Helper()
	return setupFakeProtectEnvOn(t, launchBrowser(t), attachEnv, script)
}

func setupFakeProtectPage(t *testing.T, script func(seven, allowed string) fakeScript) (*protectEnv, *fakeCDPBrowser) {
	t.Helper()
	return setupFakeProtectChoice(t, launchBrowser(t), nil, true, script)
}

func setupFakeProtectEnvOn(t *testing.T, b *chromeProc, attachEnv []string, script func(seven, allowed string) fakeScript) (*protectEnv, *fakeCDPBrowser) {
	t.Helper()
	return setupFakeProtectChoice(t, b, attachEnv, false, script)
}

func setupFakeProtectChoice(t *testing.T, b *chromeProc, attachEnv []string, pageOrigin bool, script func(seven, allowed string) fakeScript) (*protectEnv, *fakeCDPBrowser) {
	t.Helper()
	srv := pageServer(t)
	plain := plainServer(t)
	allowed := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
	protected := plain.URL
	if pageOrigin {
		protected = srv.URL
	}
	fake := newFakeCDPBrowser(t, script(protected, allowed))
	env := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: allowed, seven: srv.URL}
	env.a = startAttachEnv(t, false, attachEnv, "--cdp", b.endpoint, "--cdp-extra", fake.srv.URL, "--origin", srv.URL, "--origin", plain.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	return env, fake
}

func TestCDPHoldsExtraInfoUntilItsRequestIsKnown(t *testing.T) {
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		return afterEnable(extraInfoScript(seven, allowed))
	})
	var r struct {
		Events []extraEvent `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.extraInfo("+jsArgs("chrome-2", "orphan")+")", &r)
	b2, _ := json.Marshal(r)
	order := map[string][]string{}
	for _, e := range r.Events {
		order[e.RequestID] = append(order[e.RequestID], strings.TrimPrefix(e.Method, "Network."))
		if strings.Contains(e.Text, "secret") {
			t.Errorf("%s for %s carried seven's cookie: %s", e.Method, e.RequestID, e.Text)
		}
	}
	want := map[string]string{
		"early":  "requestWillBeSent responseReceivedExtraInfo requestWillBeSentExtraInfo responseReceived loadingFinished",
		"plain":  "requestWillBeSent responseReceived requestWillBeSentExtraInfo responseReceivedExtraInfo loadingFinished",
		"hop":    "requestWillBeSent requestWillBeSent requestWillBeSentExtraInfo responseReceivedExtraInfo responseReceived",
		"orphan": "responseReceivedExtraInfo",
	}
	for id, w := range want {
		if got := strings.Join(order[id], " "); got != w {
			t.Errorf("%s: got %q, want %q (%s)", id, got, w, b2)
		}
	}
	for _, kept := range []string{"a=kept-cookie", "a=kept-set"} {
		if !strings.Contains(string(b2), kept) {
			t.Errorf("an allowed request lost %s: %s", kept, b2)
		}
	}
}
