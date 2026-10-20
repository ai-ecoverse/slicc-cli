package integration

import (
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

type fakeCDPBrowser struct {
	srv    *httptest.Server
	script fakeScript
	mu     sync.Mutex
	got    []string
	slow   map[string]time.Duration
	pulse  []string
	gates  map[string]string
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
		switch m.Method {
		case "Target.attachToBrowserTarget":
			result = `{"sessionId":"RELAY"}`
		case "Target.getTargets":
			result = `{"targetInfos":[]}`
		default:
			result, after = f.script(m.Method, m.Params)
		}
		field := `"result":` + result
		if msg, failed := strings.CutPrefix(result, "!"); failed {
			field = fmt.Sprintf(`"error":{"code":-32000,"message":%q}`, msg)
		}
		reply := fmt.Sprintf(`{"id":%d,%s}`, m.ID, field)
		if m.SessionID != "" {
			reply = fmt.Sprintf(`{"id":%d,"sessionId":%q,%s}`, m.ID, m.SessionID, field)
		}
		f.mu.Lock()
		d := f.slow[m.Method]
		until := f.gates[m.Method]
		f.mu.Unlock()
		if until != "" {
			held[until] = append(held[until], reply)
			continue
		}
		if d > 0 {
			go func() {
				time.Sleep(d)
				_ = c.Write(ctx, websocket.MessageText, []byte(reply))
			}()
			continue
		}
		if c.Write(ctx, websocket.MessageText, []byte(reply)) != nil {
			return
		}
		for _, e := range append(after, held[m.Method]...) {
			if c.Write(ctx, websocket.MessageText, []byte(e)) != nil {
				return
			}
		}
		delete(held, m.Method)
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
	b := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	allowed := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
	fake := newFakeCDPBrowser(t, script(plain.URL, allowed))
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
