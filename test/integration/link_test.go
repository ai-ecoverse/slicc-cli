package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type linkEnv struct {
	t       *testing.T
	b       *chromeProc
	p       *page
	a       *attachProc
	stdio   bool
	echo    string
	source  string
	payload string
	closed  string
	origin  string
}

const sourceSize = 4 << 20

func setupLink(t *testing.T, stdio bool, extra ...string) *linkEnv {
	t.Helper()
	return setupLinkEnv(t, stdio, nil, extra...)
}

func setupLinkEnv(t *testing.T, stdio bool, procEnv []string, extra ...string) *linkEnv {
	t.Helper()
	b := launchBrowser(t)
	srv := pageServer(t)
	data := make([]byte, sourceSize)
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)
	env := &linkEnv{t: t, b: b, stdio: stdio, origin: srv.URL, payload: hex.EncodeToString(sum[:])}
	env.echo = serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	})
	env.source = serveTCP(t, func(c net.Conn) {
		_, _ = c.Write(data)
	})
	env.closed = closedAddr(t)
	args := []string{"--cdp", b.endpoint, "--name", "it-node"}
	if stdio {
		args = append(args, "--signal", "stdio")
	} else {
		args = append(args, "--origin", srv.URL)
	}
	env.a = startAttachEnv(t, stdio, procEnv, append(args, extra...)...)
	env.p = b.openPage(t, srv.URL+"/index.html")
	if stdio {
		relayStdio(t, env.p, env.a)
	}
	env.waitUp("")
	return env
}

func (e *linkEnv) waitUp(oldNonce string) string {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var st struct {
			Nonce string `json:"nonce"`
			Up    bool   `json:"up"`
		}
		var raw map[string]any
		e.p.eval("linkTest.status()", &raw)
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &st)
		if st.Up && st.Nonce != oldNonce {
			return st.Nonce
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("the link did not come up: page status %s", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type echoResult struct {
	Opened struct {
		LocalAddr  string `json:"localAddr"`
		RemoteAddr string `json:"remoteAddr"`
	} `json:"opened"`
	OK     bool   `json:"ok"`
	Bytes  int    `json:"bytes"`
	Shared bool   `json:"shared"`
	Reset  string `json:"reset"`
	Detail string `json:"detail"`
	Error  string `json:"error"`
}

func (e *linkEnv) echoTest(size int, shared bool) echoResult {
	e.t.Helper()
	host, port := hostPort(e.t, e.echo)
	var r echoResult
	e.p.eval("linkTest.echo("+jsArgs(host, port, size, shared)+")", &r)
	return r
}

func runLinkSuite(t *testing.T, stdio bool) {
	env := setupLink(t, stdio)
	t.Run("hello", env.testHello)
	t.Run("tcp echo with half-close", env.testEcho)
	t.Run("reset codes", env.testResets)
	t.Run("credit with a slow reader", env.testSlowReader)
	t.Run("credit violation", env.testOverCredit)
	t.Run("many concurrent streams", env.testMany)
	t.Run("stale nonce", env.testStale)
	if !stdio {
		t.Run("origin check", env.testOrigin)
	}
	t.Run("unknown frame closes the link", env.testUnknownFrame)
	t.Run("setup again after reload", env.testReload)
}

func TestLinkOverCDP(t *testing.T) {
	runLinkSuite(t, false)
}

func TestLinkOverStdio(t *testing.T) {
	runLinkSuite(t, true)
}

func (e *linkEnv) testHello(t *testing.T) {
	var h struct {
		V       int      `json:"v"`
		Role    string   `json:"role"`
		Name    string   `json:"name"`
		Host    string   `json:"host"`
		Version string   `json:"version"`
		Mode    string   `json:"mode"`
		Caps    []string `json:"caps"`
		Routes  struct {
			Exit bool `json:"exit"`
		} `json:"routes"`
		Policy struct {
			Deny    []string `json:"deny"`
			Summary string   `json:"summary"`
		} `json:"policy"`
		Browsers []helloBrowser `json:"browsers"`
	}
	e.p.eval("linkTest.ready()", &h)
	if h.V != 1 || h.Role != "node" || h.Name != "it-node" || h.Host != "it-node" || h.Version != "1.2.3-test" || h.Mode != "local" {
		t.Fatalf("hello %+v", h)
	}
	if strings.Join(h.Caps, ",") != "net,http,credentialed,ws,udp,cdp" || !h.Routes.Exit || h.Policy.Summary == "" {
		t.Fatalf("hello %+v", h)
	}
	if len(h.Browsers) != 1 || h.Browsers[0].ID != "chrome" || !h.Browsers[0].Seven {
		t.Fatalf("browsers %+v", h.Browsers)
	}
	want := fmt.Sprintf("127.0.0.1/32:%d", e.b.port)
	if !strings.Contains(strings.Join(h.Policy.Deny, " "), want) {
		t.Fatalf("policy deny %v lacks %s", h.Policy.Deny, want)
	}
}

func (e *linkEnv) testEcho(t *testing.T) {
	for _, shared := range []bool{false, true} {
		r := e.echoTest(1<<20+7, shared)
		if !r.OK || r.Bytes != 1<<20+7 || r.Shared != shared {
			t.Fatalf("shared=%v: %+v", shared, r)
		}
		if r.Opened.RemoteAddr != e.echo || !strings.HasPrefix(r.Opened.LocalAddr, "127.0.0.1:") {
			t.Fatalf("opened %+v, echo at %s", r.Opened, e.echo)
		}
	}
}

func (e *linkEnv) testResets(t *testing.T) {
	closedHost, closedPort := hostPort(t, e.closed)
	cases := []struct {
		name string
		open map[string]any
		code string
	}{
		{"refused", map[string]any{"kind": "tcp", "host": closedHost, "port": closedPort}, "ECONNREFUSED"},
		{"not found", map[string]any{"kind": "tcp", "host": "no-such-host.invalid", "port": 80}, "ENOTFOUND"},
		{"link-local", map[string]any{"kind": "tcp", "host": "169.254.169.254", "port": 80}, "EACCES"},
		{"debugging port", map[string]any{"kind": "tcp", "host": "127.0.0.1", "port": e.b.port}, "EACCES"},
		{"unknown kind", map[string]any{"kind": "nope"}, "EOPNOTSUPP"},
		{"bad open", map[string]any{"kind": "tcp"}, "EPROTO"},
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
}

func (e *linkEnv) testSlowReader(t *testing.T) {
	host, port := hostPort(t, e.source)
	var r struct {
		Bytes         int    `json:"bytes"`
		SHA256        string `json:"sha256"`
		MaxUncredited int    `json:"maxUncredited"`
		Reset         string `json:"reset"`
		Error         string `json:"error"`
	}
	e.p.eval("linkTest.slowRead("+jsArgs(host, port, 5)+")", &r)
	if r.Bytes != sourceSize || r.SHA256 != e.payload {
		t.Fatalf("slow read %+v", r)
	}
	if r.MaxUncredited > 256*1024 || r.MaxUncredited < 128*1024 {
		t.Fatalf("max uncredited %d, want the window to fill but never overflow", r.MaxUncredited)
	}
}

func (e *linkEnv) testOverCredit(t *testing.T) {
	host, port := hostPort(t, e.echo)
	var r echoResult
	e.p.eval("linkTest.overCredit("+jsArgs(host, port)+")", &r)
	if r.Reset != "EPROTO" {
		t.Fatalf("got %+v", r)
	}
}

func (e *linkEnv) testMany(t *testing.T) {
	host, port := hostPort(t, e.echo)
	var r struct {
		OK       int   `json:"ok"`
		Shared   int   `json:"shared"`
		Failures []any `json:"failures"`
	}
	e.p.eval("linkTest.many("+jsArgs(64, host, port, 100000)+")", &r)
	if r.OK != 64 || r.Shared == 0 {
		t.Fatalf("many: %+v", r)
	}
}

func (e *linkEnv) testStale(t *testing.T) {
	e.p.eval("linkTest.forgeStale()", nil)
	time.Sleep(300 * time.Millisecond)
	if r := e.echoTest(1000, false); !r.OK {
		t.Fatalf("after a stale bye: %+v", r)
	}
}

func (e *linkEnv) testOrigin(t *testing.T) {
	var r struct {
		Called bool `json:"called"`
	}
	e.p.eval("linkTest.forgeFromFrame('/frame.html')", &r)
	if !r.Called {
		t.Fatal("the same-origin iframe could not call the binding, so the check went untested")
	}
	other := strings.Replace(e.origin, "127.0.0.1", "localhost", 1)
	e.p.eval("linkTest.forgeFromFrame("+jsArgs(other+"/frame.html")+")", &r)
	time.Sleep(500 * time.Millisecond)
	if out := e.a.stderr.String(); strings.Contains(out, "could not answer") || strings.Contains(out, "forged") {
		t.Fatalf("slicc acted on a forged message:\n%s", out)
	}
	if res := e.echoTest(1000, false); !res.OK {
		t.Fatalf("after forged messages: %+v", res)
	}
}

func (e *linkEnv) testUnknownFrame(t *testing.T) {
	var r struct {
		Reason string `json:"reason"`
	}
	e.p.eval("linkTest.unknownFrame()", &r)
	if r.Reason == "still open" {
		t.Fatal("the node kept the link open after an unknown frame type")
	}
	e.a.waitStderr(t, "protocol violation", 10*time.Second)
}

func (e *linkEnv) testReload(t *testing.T) {
	var st struct {
		Nonce string `json:"nonce"`
	}
	e.p.eval("linkTest.status()", &st)
	e.p.reload()
	if e.stdio {
		e.p.rehello()
	}
	nonce := e.waitUp(st.Nonce)
	if nonce == st.Nonce || nonce == "" {
		t.Fatalf("nonce %q after reload, was %q", nonce, st.Nonce)
	}
	if r := e.echoTest(300000, false); !r.OK {
		t.Fatalf("after reload: %+v", r)
	}
}

func TestAttachIgnoresOtherOrigins(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	a := startAttach(t, "--cdp", b.endpoint)
	p := b.openPage(t, srv.URL+"/index.html")
	a.waitStderr(t, "waiting for a seven tab", 10*time.Second)
	time.Sleep(2 * time.Second)
	var st struct {
		Hellos int `json:"hellos"`
	}
	p.eval("linkTest.status()", &st)
	if st.Hellos != 0 {
		t.Fatalf("a page on %s got %d hellos", srv.URL, st.Hellos)
	}
}

func TestAttachHelp(t *testing.T) {
	out, _, code := slicc(t, "attach", "--help")
	if code != 0 || !strings.Contains(out, "--signal cdp|stdio") || !strings.Contains(out, "--origin") {
		t.Fatalf("code %d, output %q", code, out)
	}
	_, errOut, code := slicc(t, "attach")
	if code != 2 || !strings.Contains(errOut, "--cdp is required") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestAttachSurvivesSevenTabsThatVanish(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	a := startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--name", "it-node")
	a.waitStderr(t, "waiting for a seven tab", 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i := range 40 {
		var res struct {
			TargetID string `json:"targetId"`
		}
		if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": srv.URL + "/index.html?vanish=" + strconv.Itoa(i)}, &res); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i%5) * time.Millisecond)
		_ = b.client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
	}
	env := &linkEnv{t: t, b: b, a: a, origin: srv.URL}
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
}
