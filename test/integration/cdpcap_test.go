package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

type cdpEnv struct {
	*linkEnv
	extra *chromeProc
	plain *httptest.Server
	wsURL string
}

type cdpClosed struct {
	End    bool   `json:"end"`
	Reset  string `json:"reset"`
	Detail string `json:"detail"`
	Error  string `json:"error"`
}

type helloBrowser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Seven   bool   `json:"seven"`
}

func plainServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Set-Cookie", "sse=secret-set; Path=/")
		_, _ = w.Write([]byte("data: secret-sse\n\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>cdp nav " + r.URL.Query().Get("n") + "</title><p>plain"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setupCDP(t *testing.T) *cdpEnv {
	t.Helper()
	b := launchBrowser(t)
	extra := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	env := &cdpEnv{
		linkEnv: &linkEnv{t: t, b: b, origin: srv.URL},
		extra:   extra,
		plain:   plain,
		wsURL:   "ws" + strings.TrimPrefix(plain.URL, "http") + "/ws",
	}
	env.a = startAttach(t, false, "--cdp", b.endpoint, "--cdp-extra", extra.endpoint, "--origin", srv.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	return env
}

func TestCDPCapability(t *testing.T) {
	env := setupCDP(t)
	t.Run("hello lists both browsers", env.testHelloBrowsers)
	t.Run("Browser.getVersion", env.testVersion)
	t.Run("createTarget and navigate", env.testNavigate)
	t.Run("large screenshot", env.testScreenshot)
	t.Run("two concurrent clients", env.testConcurrent)
	t.Run("websocket frame events are dropped", env.testFiltered)
	t.Run("reset codes", env.testCDPResets)
	t.Run("second browser is a separate browser", env.testSecondBrowser)
	t.Run("browser exit resets and updates the list", env.testBrowserExit)
}

func (e *cdpEnv) testHelloBrowsers(t *testing.T) {
	var h struct {
		Caps     []string       `json:"caps"`
		Browsers []helloBrowser `json:"browsers"`
	}
	e.p.eval("linkTest.ready()", &h)
	if !strings.Contains(strings.Join(h.Caps, " "), "cdp") {
		t.Fatalf("caps %v lack cdp", h.Caps)
	}
	if len(h.Browsers) != 2 {
		t.Fatalf("browsers %+v", h.Browsers)
	}
	first, second := h.Browsers[0], h.Browsers[1]
	if first.ID != "chrome" || !first.Seven || second.ID != "chrome-2" || second.Seven {
		t.Fatalf("browsers %+v", h.Browsers)
	}
	for _, b := range h.Browsers {
		if b.Name == "" || b.Version == "" {
			t.Fatalf("browser %+v lacks a name or version", b)
		}
	}
}

func (e *cdpEnv) testVersion(t *testing.T) {
	var r struct {
		Opened struct {
			Product         string `json:"product"`
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"opened"`
		Product         string    `json:"product"`
		ProtocolVersion string    `json:"protocolVersion"`
		Closed          cdpClosed `json:"closed"`
		Reset           string    `json:"reset"`
		Error           string    `json:"error"`
	}
	e.p.eval("linkTest.cdp.version('chrome')", &r)
	if !strings.Contains(r.Opened.Product, "Chrome/") || r.Opened.ProtocolVersion == "" {
		t.Fatalf("opened %+v", r)
	}
	if r.Product != r.Opened.Product || r.ProtocolVersion != r.Opened.ProtocolVersion {
		t.Fatalf("Browser.getVersion %q %q, OPENED %+v", r.Product, r.ProtocolVersion, r.Opened)
	}
	if !r.Closed.End {
		t.Fatalf("closing after END: %+v", r.Closed)
	}
}

func (e *cdpEnv) testNavigate(t *testing.T) {
	var r struct {
		Title  string    `json:"title"`
		Href   string    `json:"href"`
		Closed cdpClosed `json:"closed"`
		Reset  string    `json:"reset"`
		Error  string    `json:"error"`
	}
	url := e.plain.URL + "/page?n=navigated"
	e.p.eval("linkTest.cdp.navigate("+jsArgs("chrome", url)+")", &r)
	if r.Title != "cdp nav navigated" || r.Href != url || !r.Closed.End {
		t.Fatalf("navigate: %+v", r)
	}
}

func (e *cdpEnv) testScreenshot(t *testing.T) {
	var r struct {
		TargetID   string    `json:"targetId"`
		Length     int       `json:"length"`
		SHA256     string    `json:"sha256"`
		PNG        string    `json:"png"`
		MaxMessage int       `json:"maxMessage"`
		Closed     cdpClosed `json:"closed"`
		Reset      string    `json:"reset"`
		Error      string    `json:"error"`
	}
	e.p.eval("linkTest.cdp.screenshot('chrome', 1500)", &r)
	if r.PNG != "PNG" || r.Length < 4<<20 || r.MaxMessage <= r.Length || !r.Closed.End {
		t.Fatalf("screenshot: %+v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := e.b.client.Attach(ctx, r.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = e.b.client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": r.TargetID}, nil)
	}()
	var shot struct {
		Data string `json:"data"`
	}
	clip := map[string]any{"x": 0, "y": 0, "width": 1500, "height": 1500, "scale": 1}
	if err := s.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "clip": clip, "captureBeyondViewport": true}, &shot); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(shot.Data))
	if got := hex.EncodeToString(sum[:]); got != r.SHA256 || len(shot.Data) != r.Length {
		t.Fatalf("the screenshot through the link (%d bytes, %s) differs from a direct one (%d bytes, %s)", r.Length, r.SHA256, len(shot.Data), got)
	}
}

func (e *cdpEnv) testConcurrent(t *testing.T) {
	var r struct {
		Both    bool        `json:"both"`
		Foreign string      `json:"foreign"`
		Own     int         `json:"own"`
		Closed  []cdpClosed `json:"closed"`
		Shared  []bool      `json:"shared"`
		Reset   string      `json:"reset"`
		Error   string      `json:"error"`
	}
	e.p.eval("linkTest.cdp.concurrent('chrome')", &r)
	if !r.Both || r.Foreign != "refused" || r.Own != 42 || len(r.Closed) != 2 || !r.Closed[0].End || !r.Closed[1].End {
		t.Fatalf("concurrent: %+v", r)
	}
	if len(r.Shared) != 2 || r.Shared[0] || !r.Shared[1] {
		t.Fatalf("expected one stream on its own channel and one shared: %+v", r.Shared)
	}
}

func (e *cdpEnv) directFrameEvents(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub := e.b.client.Subscribe()
	defer sub.Close()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := e.b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &res); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = e.b.client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
	}()
	s, err := e.b.client.Attach(ctx, res.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Network.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	expr := `new Promise((resolve, reject) => { const ws = new WebSocket(` + jsArgs(e.wsURL) + `); ws.onerror = reject; ws.onopen = () => ws.send('x'); ws.onmessage = () => { ws.close(); resolve(true); }; })`
	if err := s.Evaluate(ctx, expr, cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, nil); err != nil {
		t.Fatal(err)
	}
	frames := 0
	for {
		select {
		case ev := <-sub.Events():
			if ev.SessionID != s.ID {
				continue
			}
			if strings.HasPrefix(ev.Method, "Network.webSocketFrame") {
				frames++
			}
			if ev.Method == "Network.webSocketClosed" {
				return frames
			}
		case <-ctx.Done():
			t.Fatal("no Network.webSocketClosed on the direct client")
		}
	}
}

func (e *cdpEnv) testFiltered(t *testing.T) {
	if n := e.directFrameEvents(t); n == 0 {
		t.Fatal("Chrome sent no Network.webSocketFrame* events to a direct client, so the filter went untested")
	}
	var r struct {
		Echoed int            `json:"echoed"`
		Counts map[string]int `json:"counts"`
		Closed cdpClosed      `json:"closed"`
		Reset  string         `json:"reset"`
		Error  string         `json:"error"`
	}
	e.p.eval("linkTest.cdp.filtered("+jsArgs("chrome", e.wsURL, 5)+")", &r)
	if r.Echoed != 5 || r.Counts["Network.webSocketCreated"] == 0 || r.Counts["Network.webSocketClosed"] == 0 {
		t.Fatalf("filtered: %+v", r)
	}
	if r.Counts["Network.webSocketFrameSent"] != 0 || r.Counts["Network.webSocketFrameReceived"] != 0 {
		t.Fatalf("frame events reached the page: %+v", r.Counts)
	}
}

func (e *cdpEnv) testCDPResets(t *testing.T) {
	cases := []struct {
		name string
		open map[string]any
		code string
	}{
		{"unknown browser", map[string]any{"kind": "cdp", "browser": "nope"}, "ENOENT"},
		{"no browser", map[string]any{"kind": "cdp"}, "EPROTO"},
	}
	for _, c := range cases {
		for _, shared := range []bool{false, true} {
			var r echoResult
			e.p.eval("linkTest.open("+jsArgs(c.open, shared)+")", &r)
			if r.Reset != c.code {
				t.Fatalf("%s (shared=%v): got %+v, want %s", c.name, shared, r, c.code)
			}
		}
	}
	var r cdpClosed
	e.p.eval("linkTest.cdp.tooBig('chrome')", &r)
	if r.Reset != "EMSGSIZE" {
		t.Fatalf("a message over 64 MiB: %+v", r)
	}
}

func targetIDs(t *testing.T, b *chromeProc) map[string]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	infos, err := b.client.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, ti := range infos {
		out[ti.TargetID] = true
	}
	return out
}

func (e *cdpEnv) testSecondBrowser(t *testing.T) {
	var r struct {
		TargetID string    `json:"targetId"`
		Closed   cdpClosed `json:"closed"`
		Reset    string    `json:"reset"`
		Error    string    `json:"error"`
	}
	e.p.eval("linkTest.cdp.createTarget("+jsArgs("chrome-2", e.plain.URL+"/?n=second")+")", &r)
	if r.TargetID == "" || !r.Closed.End {
		t.Fatalf("createTarget on chrome-2: %+v", r)
	}
	if !targetIDs(t, e.extra)[r.TargetID] {
		t.Fatalf("target %s is not in the second browser", r.TargetID)
	}
	if targetIDs(t, e.b)[r.TargetID] {
		t.Fatalf("target %s showed up in the first browser", r.TargetID)
	}
}

func (e *cdpEnv) testBrowserExit(t *testing.T) {
	var held struct {
		Opened map[string]any `json:"opened"`
		Reset  string         `json:"reset"`
		Error  string         `json:"error"`
	}
	e.p.eval("linkTest.cdp.hold('exit', 'chrome-2')", &held)
	if held.Opened == nil {
		t.Fatalf("hold: %+v", held)
	}
	_ = e.extra.cmd.Process.Kill()
	var r cdpClosed
	e.p.eval("linkTest.cdp.held('exit')", &r)
	if r.Reset != "ECONNRESET" {
		t.Fatalf("after the browser exited: %+v", r)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var updates [][]helloBrowser
		e.p.eval("linkTest.browserUpdates()", &updates)
		if n := len(updates); n > 0 && len(updates[n-1]) == 1 && updates[n-1][0].ID == "chrome" && updates[n-1][0].Seven {
			break
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(updates)
			t.Fatalf("no browsers update without chrome-2: %s", b)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var open echoResult
	e.p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "cdp", "browser": "chrome-2"}, false)+")", &open)
	if open.Reset != "ENOENT" {
		t.Fatalf("a removed browser: %+v", open)
	}
}

func TestAttachCDPFlags(t *testing.T) {
	out, _, code := slicc(t, "attach", "--help")
	if code != 0 || !strings.Contains(out, "--cdp-extra <url>") || !strings.Contains(out, "full control") {
		t.Fatalf("code %d, help %q", code, out)
	}
	_, errOut, code := slicc(t, "attach", "--cdp", "http://127.0.0.1:1", "--cdp", "http://127.0.0.1:2")
	if code != 2 || !strings.Contains(errOut, "use --cdp-extra") {
		t.Fatalf("a second --cdp: code %d, stderr %q", code, errOut)
	}
	closed := closedAddr(t)
	_, errOut, code = slicc(t, "attach", "--signal", "stdio", "--cdp-extra", "http://"+closed)
	if code != 1 || !strings.Contains(errOut, "browser http://"+closed) {
		t.Fatalf("an extra browser that doesn't answer: code %d, stderr %q", code, errOut)
	}
}

func TestLaunchHelpKill(t *testing.T) {
	out, _, code := slicc(t, "launch", "--help")
	if code != 0 || !strings.Contains(out, "--kill") {
		t.Fatalf("code %d, help %q", code, out)
	}
}
