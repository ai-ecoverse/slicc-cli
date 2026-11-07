package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/cdpin"
	"github.com/coder/websocket"
)

type cdpSeven struct {
	b      *chromeProc
	p      *page
	origin string
}

func newCDPSeven(t *testing.T) *cdpSeven {
	t.Helper()
	return newCDPSevenDriving(t, false)
}

func newCDPSevenDriving(t *testing.T, apart bool) *cdpSeven {
	t.Helper()
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--remote-allow-origins="+srv.URL)
	p := b.openPage(t, srv.URL+"/index.html?cdpin")
	driven := b
	if apart {
		driven = launchBrowserOn(t, 0, "--remote-allow-origins="+srv.URL)
	}
	v, err := getVersion(driven.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := v["webSocketDebuggerUrl"].(string)
	if ws == "" {
		t.Fatalf("the test browser has no browser socket: %v", v)
	}
	var out any
	p.eval("linkTest.cdpIn.target("+jsArgs(ws)+")", &out)
	return &cdpSeven{b: b, p: p, origin: srv.URL}
}

type cdpInState struct {
	Opens   int      `json:"opens"`
	Live    int      `json:"live"`
	Resets  []string `json:"resets"`
	Ends    int      `json:"ends"`
	Methods []string `json:"methods"`
}

func (s *cdpSeven) state() cdpInState {
	var st cdpInState
	s.p.eval("linkTest.cdpIn.state()", &st)
	return st
}

func (s *cdpSeven) do(expr string) {
	var out any
	s.p.eval("linkTest.cdpIn."+expr, &out)
}

func (s *cdpSeven) waitState(t *testing.T, what string, ok func(cdpInState) bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st := s.state()
		if ok(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page never saw %s: %+v", what, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type cdpProc struct {
	*agentProc
	url  string
	port int
}

func (s *cdpSeven) start(t *testing.T, env []string) *cdpProc {
	t.Helper()
	p := startAgentCmd(t, env, nil, "cdp", "--cdp", s.b.endpoint, "--origin", s.origin, "--port", "0", "--json")
	return listening(t, p)
}

func listening(t *testing.T, p *agentProc) *cdpProc {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		for _, line := range strings.Split(p.stdout.String(), "\n") {
			var e struct {
				Event string `json:"event"`
				URL   string `json:"url"`
			}
			if json.Unmarshal([]byte(line), &e) == nil && e.Event == "listening" {
				_, port := hostPort(t, strings.TrimPrefix(e.URL, "http://"))
				return &cdpProc{agentProc: p, url: e.URL, port: port}
			}
		}
		select {
		case code := <-p.exited:
			p.exited <- code
			t.Fatalf("slicc cdp exited %d before it listened\nstdout:\n%s\nstderr:\n%s", code, p.stdout.String(), p.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("slicc cdp never listened\nstdout:\n%s\nstderr:\n%s", p.stdout.String(), p.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type httpAnswer struct {
	status int
	body   string
}

func cdpHTTP(t *testing.T, method, url string, header map[string]string) httpAnswer {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpAnswer{resp.StatusCode, string(b)}
}

func (c *cdpProc) get(t *testing.T, path string) httpAnswer {
	t.Helper()
	return cdpHTTP(t, http.MethodGet, c.url+path, nil)
}

func (c *cdpProc) browserSocket(t *testing.T) string {
	t.Helper()
	a := c.get(t, "/json/version")
	var v map[string]string
	if a.status != 200 || json.Unmarshal([]byte(a.body), &v) != nil {
		t.Fatalf("/json/version: %d %s", a.status, a.body)
	}
	return v["webSocketDebuggerUrl"]
}

type cdpClient struct {
	t      *testing.T
	conn   *websocket.Conn
	msgs   chan map[string]any
	closed chan error
}

func dialEndpoint(t *testing.T, url string, header http.Header) *cdpClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	conn.SetReadLimit(-1)
	c := &cdpClient{t: t, conn: conn, msgs: make(chan map[string]any, 4096), closed: make(chan error, 1)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				c.closed <- err
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				c.msgs <- m
			}
		}
	}()
	t.Cleanup(func() { _ = conn.CloseNow() })
	return c
}

func (c *cdpClient) send(m map[string]any) {
	c.t.Helper()
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *cdpClient) call(id int, method string, params map[string]any, session string) map[string]any {
	c.t.Helper()
	m := map[string]any{"id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	if session != "" {
		m["sessionId"] = session
	}
	c.send(m)
	return c.answer(id)
}

func (c *cdpClient) answer(id int) map[string]any {
	c.t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case m := <-c.msgs:
			if n, ok := m["id"].(float64); ok && int(n) == id {
				if e, ok := m["error"]; ok {
					c.t.Fatalf("call %d: %v", id, e)
				}
				return m
			}
		case err := <-c.closed:
			c.t.Fatalf("socket closed before answer %d: %v", id, err)
		case <-deadline:
			c.t.Fatalf("no answer %d", id)
		}
	}
}

func (c *cdpClient) closeStatus(within time.Duration) (websocket.StatusCode, string) {
	c.t.Helper()
	for {
		select {
		case <-c.msgs:
		case err := <-c.closed:
			var ce websocket.CloseError
			if ok := asCloseError(err, &ce); ok {
				return ce.Code, ce.Reason
			}
			return -1, err.Error()
		case <-time.After(within):
			c.t.Fatalf("the socket stayed open for %s", within)
			return 0, ""
		}
	}
}

func asCloseError(err error, out *websocket.CloseError) bool {
	if websocket.CloseStatus(err) == -1 {
		return false
	}
	for e := err; e != nil; {
		if ce, ok := e.(websocket.CloseError); ok {
			*out = ce
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	out.Code = websocket.CloseStatus(err)
	return true
}

func cdpResult(m map[string]any) map[string]any {
	r, _ := m["result"].(map[string]any)
	return r
}

var browserSocketURL = regexp.MustCompile(`^ws://127\.0\.0\.1:\d+/devtools/browser/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestCDPEndpointDiscovery(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	a := c.get(t, "/json/version")
	var v map[string]string
	if a.status != 200 || json.Unmarshal([]byte(a.body), &v) != nil {
		t.Fatalf("/json/version: %d %s", a.status, a.body)
	}
	for _, k := range []string{"Browser", "Protocol-Version", "User-Agent", "V8-Version", "WebKit-Version", "webSocketDebuggerUrl"} {
		if v[k] == "" {
			t.Fatalf("/json/version lacks %s: %v", k, v)
		}
	}
	if !browserSocketURL.MatchString(v["webSocketDebuggerUrl"]) || !strings.Contains(v["webSocketDebuggerUrl"], ":"+strconv.Itoa(c.port)+"/") {
		t.Fatalf("webSocketDebuggerUrl %q", v["webSocketDebuggerUrl"])
	}
	local := cdpHTTP(t, http.MethodGet, c.url+"/json/version", map[string]string{"Host": "localhost:" + strconv.Itoa(c.port)})
	if !strings.Contains(local.body, `"ws://localhost:`+strconv.Itoa(c.port)+`/devtools/browser/`) {
		t.Fatalf("the URL doesn't echo the Host: %s", local.body)
	}
	newTab := cdpHTTP(t, http.MethodPut, c.url+"/json/new?"+s.origin+"/frame.html", nil)
	var created map[string]string
	if newTab.status != 200 || json.Unmarshal([]byte(newTab.body), &created) != nil || created["id"] == "" || created["type"] != "page" {
		t.Fatalf("/json/new: %d %s", newTab.status, newTab.body)
	}
	if created["webSocketDebuggerUrl"] != fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", c.port, created["id"]) {
		t.Fatalf("/json/new entry %v", created)
	}
	for _, path := range []string{"/json", "/json/list"} {
		a := c.get(t, path)
		var list []map[string]string
		if a.status != 200 || json.Unmarshal([]byte(a.body), &list) != nil || len(list) == 0 {
			t.Fatalf("%s: %d %s", path, a.status, a.body)
		}
		found := false
		for _, e := range list {
			if e["type"] == "browser" || e["type"] == "tab" {
				t.Fatalf("%s lists a %s target: %v", path, e["type"], e)
			}
			if _, ok := e["description"]; !ok || e["id"] == "" || e["webSocketDebuggerUrl"] != fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", c.port, e["id"]) {
				t.Fatalf("%s entry %v", path, e)
			}
			found = found || e["id"] == created["id"]
		}
		if !found {
			t.Fatalf("%s lacks the new target %s: %s", path, created["id"], a.body)
		}
	}
	if a := c.get(t, "/json/activate/"+created["id"]); a.status != 200 || a.body != "Target activated" {
		t.Fatalf("/json/activate: %d %q", a.status, a.body)
	}
	if a := c.get(t, "/json/close/"+created["id"]); a.status != 200 || a.body != "Target is closing" {
		t.Fatalf("/json/close: %d %q", a.status, a.body)
	}
	if a := c.get(t, "/json/close/no-such-target"); a.status != 500 || a.body == "" {
		t.Fatalf("/json/close of an unknown target: %d %q", a.status, a.body)
	}
	if a := c.get(t, "/json/protocol"); a.status != 404 {
		t.Fatalf("/json/protocol: %d %q", a.status, a.body)
	}
	if a := c.get(t, "/devtools/browser/not-this-one"); a.status != 404 {
		t.Fatalf("a wrong browser path: %d %q", a.status, a.body)
	}
	if out := c.stderr.String(); strings.Contains(out, "frame.html") {
		t.Fatalf("stderr names a target URL:\n%s", out)
	}
}

func TestCDPBrowserSocketCreatesAttachesEvaluatesAndCloses(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	cl := dialEndpoint(t, c.browserSocket(t), nil)
	target, _ := cdpResult(cl.call(41, "Target.createTarget", map[string]any{"url": "about:blank"}, ""))["targetId"].(string)
	if target == "" {
		t.Fatal("no target")
	}
	session, _ := cdpResult(cl.call(42, "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, ""))["sessionId"].(string)
	if session == "" {
		t.Fatal("no session")
	}
	got := cl.call(43, "Runtime.evaluate", map[string]any{"expression": "6 * 7", "returnByValue": true}, session)
	if got["sessionId"] != session {
		t.Fatalf("the answer lost its session: %v", got)
	}
	if r, _ := cdpResult(got)["result"].(map[string]any); r["value"] != float64(42) {
		t.Fatalf("evaluate: %v", got)
	}
	cl.call(44, "Target.closeTarget", map[string]any{"targetId": target}, "")
	st := s.state()
	for _, m := range []string{"Target.createTarget", "Target.attachToTarget", "Runtime.evaluate", "Target.closeTarget"} {
		if !contains(st.Methods, m) {
			t.Fatalf("the page never got %s: %v", m, st.Methods)
		}
	}
	bin := dialEndpoint(t, c.browserSocket(t), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := bin.conn.Write(ctx, websocket.MessageBinary, []byte(`{"id":1,"method":"Browser.getVersion"}`)); err != nil {
		t.Fatal(err)
	}
	if code, _ := bin.closeStatus(30 * time.Second); code != websocket.StatusUnsupportedData {
		t.Fatalf("a binary message closed with %d, want 1003", code)
	}
}

func TestCDPPageSocketAddsAndStripsTheSession(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	a := cdpHTTP(t, http.MethodPut, c.url+"/json/new?about:blank", nil)
	var created map[string]string
	if a.status != 200 || json.Unmarshal([]byte(a.body), &created) != nil {
		t.Fatalf("/json/new: %d %s", a.status, a.body)
	}
	cl := dialEndpoint(t, created["webSocketDebuggerUrl"], nil)
	got := cl.call(1, "Runtime.evaluate", map[string]any{"expression": "1 + 1", "returnByValue": true}, "")
	if _, ok := got["sessionId"]; ok {
		t.Fatalf("a page socket answer carries a sessionId: %v", got)
	}
	if r, _ := cdpResult(got)["result"].(map[string]any); r["value"] != float64(2) {
		t.Fatalf("evaluate: %v", got)
	}
	cl.call(2, "Page.enable", nil, "")
	cl.call(3, "Page.navigate", map[string]any{"url": s.origin + "/frame.html"}, "")
	deadline := time.Now().Add(30 * time.Second)
	for {
		r := cl.call(4, "Runtime.evaluate", map[string]any{"expression": "location.pathname", "returnByValue": true}, "")
		if v, _ := cdpResult(r)["result"].(map[string]any); v["value"] == "/frame.html" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page socket never navigated: %v", r)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if a := c.get(t, "/json/close/"+created["id"]); a.status != 200 {
		t.Fatalf("/json/close: %d %s", a.status, a.body)
	}
	if code, reason := cl.closeStatus(30 * time.Second); code != websocket.StatusNormalClosure {
		t.Fatalf("closing the target closed the page socket with %d %q, want 1000", code, reason)
	}
	missing := dialEndpoint(t, fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/no-such-target", c.port), nil)
	if code, reason := missing.closeStatus(30 * time.Second); code != websocket.StatusInternalError || reason == "" {
		t.Fatalf("an unknown target closed with %d %q, want 1011 with the error", code, reason)
	}
}

func TestCDPTwoClientsAreIsolated(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	url := c.browserSocket(t)
	a, b := dialEndpoint(t, url, nil), dialEndpoint(t, url, nil)
	ta, _ := cdpResult(a.call(1, "Target.createTarget", map[string]any{"url": "about:blank"}, ""))["targetId"].(string)
	tb, _ := cdpResult(b.call(1, "Target.createTarget", map[string]any{"url": "about:blank"}, ""))["targetId"].(string)
	sa, _ := cdpResult(a.call(2, "Target.attachToTarget", map[string]any{"targetId": ta, "flatten": true}, ""))["sessionId"].(string)
	sb, _ := cdpResult(b.call(2, "Target.attachToTarget", map[string]any{"targetId": tb, "flatten": true}, ""))["sessionId"].(string)
	a.send(map[string]any{"id": 3, "method": "Runtime.evaluate", "sessionId": sa, "params": map[string]any{"expression": "'a'", "returnByValue": true}})
	b.send(map[string]any{"id": 3, "method": "Runtime.evaluate", "sessionId": sb, "params": map[string]any{"expression": "'b'", "returnByValue": true}})
	for name, cl := range map[string]*cdpClient{"a": a, "b": b} {
		got := cl.answer(3)
		if v, _ := cdpResult(got)["result"].(map[string]any); v["value"] != name {
			t.Fatalf("client %s got %v", name, got)
		}
	}
	a.call(4, "Runtime.enable", nil, sa)
	time.Sleep(500 * time.Millisecond)
	for {
		select {
		case m := <-b.msgs:
			if m["sessionId"] == sa {
				t.Fatalf("client b saw client a's session: %v", m)
			}
			continue
		default:
		}
		break
	}
	if n := c.stdout.String(); !strings.Contains(n, `"event":"clients","count":2`) {
		t.Fatalf("no clients event for two sockets:\n%s", n)
	}
}

func TestCDPRefusesAMessageOver64MiB(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	cl := dialEndpoint(t, c.browserSocket(t), nil)
	cl.call(1, "Browser.getVersion", nil, "")
	big := make([]byte, 64<<20+1)
	copy(big, `{"id":2,"method":"Runtime.evaluate","params":{"expression":"`)
	for i := 60; i < len(big)-3; i++ {
		big[i] = 'x'
	}
	copy(big[len(big)-3:], `"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = cl.conn.Write(ctx, websocket.MessageText, big)
	if code, _ := cl.closeStatus(60 * time.Second); code != websocket.StatusMessageTooBig {
		t.Fatalf("a 64 MiB+ message closed with %d, want 1009", code)
	}
	s.waitState(t, "an EMSGSIZE reset", func(st cdpInState) bool { return contains(st.Resets, "EMSGSIZE") })
	if a := c.get(t, "/json/version"); a.status != 200 {
		t.Fatalf("the endpoint didn't survive: %d %s", a.status, a.body)
	}
}

func TestCDPChecksHostAndOrigin(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	port := strconv.Itoa(c.port)
	for _, host := range []string{"example.com", "example.com:" + port, "127.0.0.1.nip.io:" + port, "192.168.1.2:" + port} {
		a := cdpHTTP(t, http.MethodGet, c.url+"/json/version", map[string]string{"Host": host})
		if a.status != 403 || a.body != "Host header is specified and is not an IP address or localhost." {
			t.Fatalf("Host %s: %d %q", host, a.status, a.body)
		}
	}
	for _, host := range []string{"127.0.0.1", "localhost:" + port, "[::1]:" + port} {
		if a := cdpHTTP(t, http.MethodGet, c.url+"/json/version", map[string]string{"Host": host}); a.status != 200 {
			t.Fatalf("Host %s: %d %q", host, a.status, a.body)
		}
	}
	if a := cdpHTTP(t, http.MethodGet, c.url+"/json/version", map[string]string{"Origin": "https://example.com"}); a.status != 403 {
		t.Fatalf("plain HTTP from a web page: %d %q", a.status, a.body)
	}
	url := c.browserSocket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://example.com"}}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("an upgrade from https://example.com: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	before := s.state().Opens
	for _, origin := range []string{"", "http://127.0.0.1:" + port, "http://localhost:" + port} {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		cl := dialEndpoint(t, url, h)
		cl.call(1, "Browser.getVersion", nil, "")
		_ = cl.conn.Close(websocket.StatusNormalClosure, "")
	}
	if st := s.state(); st.Opens-before != 3 {
		t.Fatalf("the refused upgrade reached seven: %d opens, want 3", st.Opens-before)
	}
}

func TestCDPStartupRefusals(t *testing.T) {
	s := newCDPSeven(t)
	cases := []struct {
		refuse string
		code   int
		want   string
	}{
		{"EACCES: browser control from this device is off; turn on Browser for it in seven's Linked devices", 3, "slicc cdp: browser control from this device is off; turn on Browser for it in seven's Linked devices\n"},
		{"ENOENT: no browser to drive: install slicc-extension, or link a computer with npx sliccy@next", 1, "slicc cdp: no browser to drive: install slicc-extension, or link a computer with npx sliccy@next\n"},
	}
	for _, c := range cases {
		s.do("refuse(" + jsArgs(c.refuse) + ")")
		p := startAgentCmd(t, nil, nil, "cdp", "--cdp", s.b.endpoint, "--origin", s.origin, "--port", "0")
		if code := p.wait(t, 90*time.Second); code != c.code || !strings.HasSuffix(p.stderr.String(), c.want) {
			t.Fatalf("%s: exit %d, stderr %q", c.refuse, code, p.stderr.String())
		}
		if strings.Contains(p.stderr.String(), "serving") {
			t.Fatalf("%s: slicc said it was serving:\n%s", c.refuse, p.stderr.String())
		}
	}
	other := pageServer(t)
	s.b.openPage(t, other.URL+"/index.html")
	p := startAgentCmd(t, nil, nil, "cdp", "--cdp", s.b.endpoint, "--origin", other.URL, "--port", "0")
	if code := p.wait(t, 90*time.Second); code != 1 || !strings.HasSuffix(p.stderr.String(), "slicc cdp: this seven can't share its browser yet; update it\n") {
		t.Fatalf("a seven without cdp-in: exit %d, stderr %q", code, p.stderr.String())
	}
}

func TestCDPLaterRefusalKeepsTheListener(t *testing.T) {
	s := newCDPSeven(t)
	c := s.start(t, nil)
	cl := dialEndpoint(t, c.browserSocket(t), nil)
	cl.call(1, "Browser.getVersion", nil, "")
	s.do(`revoke("EACCES: the owner took this access back")`)
	if code, reason := cl.closeStatus(30 * time.Second); code != websocket.StatusPolicyViolation || reason != "the owner took this access back" {
		t.Fatalf("a revoked socket closed with %d %q, want 1008", code, reason)
	}
	if a := c.get(t, "/json/version"); a.status != 403 || a.body != "the owner took this access back" {
		t.Fatalf("/json/version after the revoke: %d %q", a.status, a.body)
	}
	if a := c.get(t, "/json/list"); a.status != 403 {
		t.Fatalf("/json/list after the revoke: %d %q", a.status, a.body)
	}
	s.do("refuse(null)")
	again := dialEndpoint(t, c.browserSocket(t), nil)
	again.call(1, "Browser.getVersion", nil, "")
}

func TestCDPStopClosesTheSocketsAndShowsInTheTray(t *testing.T) {
	trayRuntime(t)
	s := newCDPSeven(t)
	env := append(configEnv(t), "SLICC_NO_TELEMETRY=1")
	c := s.start(t, env)
	row := cdpTrayRow(t, env, c.cmd.Process.Pid, 0)
	if row.Mode != "cdp" || len(row.Offers) != 1 || row.Offers[0] != "cdp:"+strconv.Itoa(c.port) || row.CDP == nil || row.CDP.Port != c.port {
		t.Fatalf("tray row %+v", row)
	}
	cl := dialEndpoint(t, c.browserSocket(t), nil)
	cl.call(1, "Browser.getVersion", nil, "")
	cdpTrayRow(t, env, c.cmd.Process.Pid, 1)
	out, _, _ := sliccEnv(t, env, "tray")
	if !regexp.MustCompile(strconv.Itoa(c.cmd.Process.Pid) + `\s+cdp\s+link test page\s+-\s+\S+.*\s+cdp:` + strconv.Itoa(c.port)).MatchString(out) {
		t.Fatalf("slicc tray:\n%s", out)
	}
	ends := s.state().Ends
	if err := interrupt(c.cmd); err != nil {
		t.Fatal(err)
	}
	if code, _ := cl.closeStatus(30 * time.Second); code != websocket.StatusGoingAway {
		t.Fatalf("stopping closed the socket with %d, want 1001", code)
	}
	if code := c.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s.waitState(t, "the stream's END", func(st cdpInState) bool { return st.Ends > ends })
	if !strings.Contains(c.stdout.String(), `"event":"closed"`) {
		t.Fatalf("no closed event:\n%s", c.stdout.String())
	}
}

type cdpRow struct {
	PID    int      `json:"pid"`
	Mode   string   `json:"mode"`
	State  string   `json:"state"`
	Offers []string `json:"offers"`
	CDP    *struct {
		Port    int `json:"port"`
		Clients int `json:"clients"`
	} `json:"cdp"`
}

func cdpTrayRow(t *testing.T, env []string, pid, clients int) cdpRow {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, errOut, code := sliccEnv(t, env, "tray", "--json")
		var rows []cdpRow
		if code != 0 || json.Unmarshal([]byte(out), &rows) != nil {
			t.Fatalf("slicc tray --json: %d %q %q", code, out, errOut)
		}
		for _, r := range rows {
			if r.PID == pid && r.CDP != nil && r.CDP.Clients == clients {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no cdp row for %d with %d clients: %s", pid, clients, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCDPPortIsADebuggingPortForOtherSliccs(t *testing.T) {
	trayRuntime(t)
	s := newCDPSeven(t)
	p := s.p
	env := append(configEnv(t), "SLICC_DEBUG=1", "SLICC_NO_TELEMETRY=1")
	c := s.start(t, env)
	h := newFakeHub(t)
	tr := h.addTray("follow")
	newTrayLeader(t, h, p, tr)
	echo := serveTCP(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })
	_, echoPort := hostPort(t, echo)
	fp := startFollow(t, env, h.joinURL(tr), "--exit", "--allow", "127.0.0.0/8")
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var ok struct {
		OK    bool   `json:"ok"`
		Reset string `json:"reset"`
	}
	p.eval("linkTest.echo('127.0.0.1', "+strconv.Itoa(echoPort)+", 64)", &ok)
	if !ok.OK {
		t.Fatalf("follow --exit --allow 127.0.0.0/8 couldn't reach an ordinary local port: %+v", ok)
	}
	var refused struct {
		Reset  string `json:"reset"`
		Detail string `json:"detail"`
	}
	p.eval("linkTest.echo('127.0.0.1', "+strconv.Itoa(c.port)+", 64)", &refused)
	if refused.Reset != "EACCES" || !strings.Contains(refused.Detail, "a browser debugging port") {
		t.Fatalf("seven reached slicc cdp's port through follow --exit: %+v", refused)
	}
}

func TestCDPReconnectsAfterTheLinkDrops(t *testing.T) {
	p := newCDPSeven(t).p
	var out any
	env := append(configEnv(t), "SLICC_DEBUG=1", "SLICC_NO_TELEMETRY=1")
	h := newFakeHub(t)
	tr := h.addTray("cdp")
	newTrayLeader(t, h, p, tr)
	join := h.joinURL(tr)
	proc := startAgentCmd(t, env, strings.NewReader(join+"\n"), "-", "cdp", "--port", "0", "--json")
	c := listening(t, proc)
	if !strings.Contains(c.stderr.String(), "slicc: serving seven's browser at "+c.url+" (link test page)\n") {
		t.Fatalf("stderr:\n%s", c.stderr.String())
	}
	cl := dialEndpoint(t, c.browserSocket(t), nil)
	cl.call(1, "Browser.getVersion", nil, "")
	p.eval("linkTest.drop()", &out)
	if code, reason := cl.closeStatus(30 * time.Second); code != websocket.StatusInternalError || reason != "lost the link to seven" {
		t.Fatalf("a dropped link closed the socket with %d %q, want 1011", code, reason)
	}
	sawGateway := false
	deadline := time.Now().Add(90 * time.Second)
	for {
		a := c.get(t, "/json/version")
		if a.status == 502 && a.body == "no link to seven" {
			sawGateway = true
		}
		if a.status == 200 && sawGateway {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw 502 then 200 (last %d %q, saw 502 %v)\n%s", a.status, a.body, sawGateway, c.stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	again := dialEndpoint(t, c.browserSocket(t), nil)
	again.call(1, "Browser.getVersion", nil, "")
	if !strings.Contains(c.stdout.String(), `"event":"link-lost"`) || strings.Count(c.stdout.String(), `"event":"linked"`) < 2 {
		t.Fatalf("lifecycle events:\n%s", c.stdout.String())
	}
	for _, leak := range []string{join, "frame.html"} {
		if strings.Contains(c.stderr.String()+c.stdout.String(), leak) {
			t.Fatalf("output leaks %q", leak)
		}
	}
	assertRedacted(t, c.stderr.String(), h, tr)
	sc := bufio.NewScanner(strings.NewReader(c.stdout.String()))
	for sc.Scan() {
		if !json.Valid(sc.Bytes()) {
			t.Fatalf("stdout line isn't JSON: %q", sc.Text())
		}
	}
}

func nonLoopbackAddr(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			return ipn.IP.String()
		}
	}
	t.Skip("no address on this machine besides loopback")
	return ""
}

func TestCDPGuardRefusesPeersThatArentLoopback(t *testing.T) {
	for _, c := range []struct {
		addr string
		want bool
	}{{"127.0.0.1:1", true}, {"127.8.9.10:1", true}, {"[::1]:1", true}, {"10.0.0.2:1", false}, {"[fd00::1]:1", false}, {"192.168.1.2:1", false}} {
		a, err := net.ResolveTCPAddr("tcp", c.addr)
		if err != nil {
			t.Fatal(err)
		}
		if cdpin.Loopback(a) != c.want {
			t.Fatalf("Loopback(%s) = %v", c.addr, !c.want)
		}
	}
	other := nonLoopbackAddr(t)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	g := cdpin.Guard(ln, nil)
	t.Cleanup(func() { _ = g.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := g.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	far, err := net.Dial("tcp4", net.JoinHostPort(other, port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = far.Close() }()
	_, _ = far.Write([]byte("GET /json/version HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"))
	_ = far.SetReadDeadline(time.Now().Add(10 * time.Second))
	if n, err := far.Read(make([]byte, 64)); err == nil || n > 0 {
		t.Fatalf("a peer at %s got %d bytes back (%v)", other, n, err)
	}
	near, err := net.Dial("tcp4", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = near.Close() }()
	select {
	case c := <-accepted:
		if !cdpin.Loopback(c.RemoteAddr()) {
			t.Fatalf("accepted %s", c.RemoteAddr())
		}
		_ = c.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("a loopback peer wasn't accepted")
	}
	select {
	case c := <-accepted:
		t.Fatalf("accepted a second peer %s", c.RemoteAddr())
	default:
	}
}

func TestCDPUsageAndAPortThatIsTaken(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"cdp"}, 2, "give a join URL (slicc <join-url> cdp) or --cdp <url>"},
		{[]string{"cdp", "--port", "70000", "--signal", "stdio"}, 2, "--port needs a port from 0 to 65535"},
		{[]string{"cdp", "--signal", "smoke"}, 2, "--signal must be cdp or stdio"},
		{[]string{"cdp", "--signal", "stdio", "extra"}, 2, "unexpected arguments"},
		{[]string{"http://127.0.0.1:1/join/tok.00112233445566778899", "cdp", "--cdp", "x"}, 2, "flag provided but not defined"},
	} {
		_, errOut, code := slicc(t, c.args...)
		if code != c.code || !strings.Contains(errOut, c.want) || strings.Contains(errOut, "tok.00112233445566778899") {
			t.Fatalf("%v: exit %d, %q", c.args, code, errOut)
		}
	}
	out, _, code := slicc(t, "cdp", "--help")
	if code != 0 || !strings.Contains(out, "usage: slicc <join-url> cdp [--port <n>] [--json]") {
		t.Fatalf("--help: %d %q", code, out)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	p := startAgentCmd(t, nil, strings.NewReader(""), "cdp", "--signal", "stdio", "--port", port, "--json")
	if code := p.wait(t, 30*time.Second); code != 1 || !strings.Contains(p.stderr.String(), "slicc cdp: port "+port+" is taken; try --port\n") {
		t.Fatalf("a taken port: exit %d, stderr %q", code, p.stderr.String())
	}
	if !strings.Contains(p.stdout.String(), `"event":"error"`) || !strings.Contains(p.stdout.String(), `"event":"closed"`) {
		t.Fatalf("--json on a taken port:\n%s", p.stdout.String())
	}
}

func TestCDPStockClientsDriveTheBrowser(t *testing.T) {
	node := requireNode(t)
	s := newCDPSevenDriving(t, true)
	c := s.start(t, nil)
	for _, which := range []string{"puppeteer", "playwright"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		cmd := exec.CommandContext(ctx, node, "testdata/stock-clients.mjs", c.url, s.origin+"/cdpin.html", which)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !strings.Contains(string(out), which+" ok") {
			t.Fatalf("%s: %v\n%s\nslicc stderr:\n%s", which, err, out, c.stderr.String())
		}
	}
	if st := s.state(); st.Opens < 4 {
		t.Fatalf("the clients didn't go through the endpoint: %+v", st)
	}
}
