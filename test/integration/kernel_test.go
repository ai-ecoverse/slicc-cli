package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type kernelEnv struct {
	*linkEnv
	port int
	addr string
}

type kernelReply struct {
	Port   int    `json:"port"`
	Stream int    `json:"stream"`
	Served int    `json:"served"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Host   string `json:"host"`
	Body   string `json:"body"`
}

func freePort(t *testing.T) int {
	t.Helper()
	_, port := hostPort(t, closedAddr(t))
	return port
}

func startKernel(t *testing.T, extra ...string) *kernelEnv {
	t.Helper()
	b := launchBrowser(t)
	srv := pageServer(t)
	port := freePort(t)
	args := append([]string{"--cdp", b.endpoint, "--name", "it-node", "--origin", srv.URL, "--kernel-port", strconv.Itoa(port)}, extra...)
	env := &kernelEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, port: port, addr: fmt.Sprintf("127.0.0.1:%d", port)}
	env.echo = serveTCP(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	env.a = startAttach(t, false, args...)
	env.p = b.openPage(t, srv.URL+"/index.html?kernel")
	env.waitUp("")
	return env
}

type rawAnswer struct {
	status int
	header http.Header
	close  bool
	body   string
}

func (e *kernelEnv) raw(t *testing.T, request string) rawAnswer {
	t.Helper()
	c, err := net.DialTimeout("tcp", e.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("reading the answer to %q: %v", request[:min(len(request), 80)], err)
	}
	body, _ := io.ReadAll(resp.Body)
	return rawAnswer{status: resp.StatusCode, header: resp.Header, close: resp.Close, body: string(body)}
}

func (e *kernelEnv) get(t *testing.T, host, path string) rawAnswer {
	t.Helper()
	return e.raw(t, "GET "+path+" HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
}

func (e *kernelEnv) host(kernelPort int) string {
	return fmt.Sprintf("%d.kernel.localhost:%d", kernelPort, e.port)
}

func wantProxyError(t *testing.T, a rawAnswer, status int, body string) {
	t.Helper()
	if a.status != status || a.body != body+"\n" {
		t.Fatalf("got %d %q, want %d %q", a.status, a.body, status, body)
	}
	if a.header.Get("X-Proxy-Error") != "1" || !a.close || !strings.HasPrefix(a.header.Get("Content-Type"), "text/plain") {
		t.Fatalf("headers %v", a.header)
	}
}

func TestKernelListener(t *testing.T) {
	e := startKernel(t)
	e.a.waitStderr(t, fmt.Sprintf("kernel services on http://<port>.kernel.localhost:%d/", e.port), 10*time.Second)
	t.Run("policy protects the kernel port", e.testKernelPolicy)
	t.Run("http request", e.testKernelHTTP)
	t.Run("keep-alive and half-close", e.testKernelKeepAlive)
	t.Run("navigation and websocket from Chrome", e.testKernelChrome)
	t.Run("host parsing", e.testKernelHosts)
	t.Run("malformed heads", e.testKernelHeads)
	t.Run("page refusals", e.testKernelRefusals)
	t.Run("9222 refused before any OPEN", e.testKernel9222)
	t.Run("no OPENED within 10 s", e.testKernelTimeout)
	t.Run("no page connected", e.testKernelNoPage)
}

func (e *kernelEnv) testKernelPolicy(t *testing.T) {
	var h struct {
		Policy struct {
			Deny []string `json:"deny"`
		} `json:"policy"`
	}
	e.p.eval("linkTest.ready()", &h)
	want := fmt.Sprintf("127.0.0.1/32:%d", e.port)
	if !slices.Contains(h.Policy.Deny, want) {
		t.Fatalf("policy deny %v lacks %s", h.Policy.Deny, want)
	}
	var r echoResult
	e.p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": "127.0.0.1", "port": e.port})+")", &r)
	if r.Reset != "EACCES" {
		t.Fatalf("tcp to the kernel port: %+v", r)
	}
}

func decodeReply(t *testing.T, a rawAnswer) kernelReply {
	t.Helper()
	var r kernelReply
	if a.status != 200 || json.Unmarshal([]byte(a.body), &r) != nil {
		t.Fatalf("got %d %q", a.status, a.body)
	}
	return r
}

func (e *kernelEnv) testKernelHTTP(t *testing.T) {
	r := decodeReply(t, e.get(t, e.host(5173), "/hello?x=1"))
	if r.Port != 5173 || r.Method != "GET" || r.Path != "/hello?x=1" || r.Host != e.host(5173) {
		t.Fatalf("reply %+v", r)
	}
	body := strings.Repeat("b", 3000)
	a := e.raw(t, "POST /up HTTP/1.1\r\nHost: "+e.host(5173)+"\r\nContent-Length: 3000\r\nConnection: close\r\n\r\n"+body)
	if r := decodeReply(t, a); r.Method != "POST" || r.Body != body {
		t.Fatalf("post reply %+v", r)
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", e.addr)
	}}}
	resp, err := client.Post("http://"+e.host(5173)+"/client", "text/plain", strings.NewReader(strings.Repeat("z", 200000)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if r := decodeReply(t, rawAnswer{status: resp.StatusCode, body: string(b)}); len(r.Body) != 200000 {
		t.Fatalf("client post: %d bytes", len(r.Body))
	}
}

func (e *kernelEnv) testKernelKeepAlive(t *testing.T) {
	c, err := net.Dial("tcp", e.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(c)
	var first kernelReply
	for i := 1; i <= 2; i++ {
		_, _ = io.WriteString(c, "GET /"+strconv.Itoa(i)+" HTTP/1.1\r\nHost: "+e.host(5173)+"\r\n\r\n")
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		r := decodeReply(t, rawAnswer{status: resp.StatusCode, body: string(b)})
		if i == 1 {
			first = r
		}
		if r.Served != i || r.Stream != first.Stream {
			t.Fatalf("request %d: %+v, first %+v", i, r, first)
		}
	}
	_ = c.(*net.TCPConn).CloseWrite()
	rest, err := io.ReadAll(br)
	if err != nil || len(rest) != 0 {
		t.Fatalf("after half-close: %q %v", rest, err)
	}
}

func (e *kernelEnv) testKernelChrome(t *testing.T) {
	url := "http://" + e.host(5173) + "/nav"
	p := e.b.openTarget(t, url)
	p.waitFor("location.href === "+jsArgs(url)+" && document.readyState === 'complete' && document.body?.innerText.length > 0", 15*time.Second)
	var text string
	p.eval("document.body.innerText", &text)
	var r kernelReply
	if json.Unmarshal([]byte(text), &r) != nil || r.Path != "/nav" || r.Host != e.host(5173) {
		t.Fatalf("navigation to %s showed %q", url, text)
	}
	var ws struct {
		Text  string `json:"text"`
		Bytes []int  `json:"bytes"`
		Error string `json:"error"`
	}
	p.eval(`new Promise((resolve) => {
		const ws = new WebSocket('ws://`+e.host(5173)+`/ws');
		ws.binaryType = 'arraybuffer';
		const out = {};
		ws.onerror = () => resolve({ error: 'websocket error' });
		ws.onopen = () => ws.send('hi there');
		ws.onmessage = (ev) => {
			if (typeof ev.data === 'string') { out.text = ev.data; ws.send(new Uint8Array([1, 2, 3, 250])); }
			else { out.bytes = [...new Uint8Array(ev.data)]; ws.close(1000); resolve(out); }
		};
	})`, &ws)
	if ws.Text != "echo: hi there" || !slices.Equal(ws.Bytes, []int{1, 2, 3, 250}) {
		t.Fatalf("websocket %+v", ws)
	}
}

func (e *kernelEnv) testKernelHosts(t *testing.T) {
	ok := []string{
		"5173.kernel.localhost",
		"5173.KERNEL.LocalHost",
		e.host(5173),
	}
	for _, h := range ok {
		if r := decodeReply(t, e.get(t, h, "/")); r.Port != 5173 {
			t.Fatalf("%s: %+v", h, r)
		}
	}
	bad := []string{
		"localhost",
		fmt.Sprintf("localhost:%d", e.port),
		"kernel.localhost",
		"05173.kernel.localhost",
		"0.kernel.localhost",
		"65536.kernel.localhost",
		"100000.kernel.localhost",
		"5173.kernel.localhost.",
		fmt.Sprintf("5173.kernel.localhost.:%d", e.port),
		fmt.Sprintf("5173.kernel.localhost:0%d", e.port),
		fmt.Sprintf("5173.kernel.localhost:%d", e.port+1),
		"5173.kernel.localhost:",
		"x.5173.kernel.localhost",
		"5173.kernel.localhost.evil.com",
		"127.0.0.1",
	}
	for _, h := range bad {
		wantProxyError(t, e.get(t, h, "/"), 421, "only <port>.kernel.localhost is served here")
	}
	wantProxyError(t, e.get(t, "65535.kernel.localhost", "/"), 502, "nothing listening on kernel port 65535")
	wantProxyError(t, e.get(t, "1.kernel.localhost", "/"), 502, "nothing listening on kernel port 1")
}

func (e *kernelEnv) testKernelHeads(t *testing.T) {
	bad := []string{
		"GET / HTTP/1.1\r\n\r\n",
		"GET / HTTP/1.1\r\nHost: " + e.host(5173) + "\r\nHost: " + e.host(5173) + "\r\n\r\n",
		"GET / HTTP/2.0\r\nHost: " + e.host(5173) + "\r\n\r\n",
		"GET /  HTTP/1.1\r\nHost: " + e.host(5173) + "\r\n\r\n",
		"G@T / HTTP/1.1\r\nHost: " + e.host(5173) + "\r\n\r\n",
		"hello\r\n\r\n",
	}
	for _, req := range bad {
		wantProxyError(t, e.raw(t, req), 400, "malformed request head")
	}
	big := "GET / HTTP/1.1\r\nHost: " + e.host(5173) + "\r\nX-Big: " + strings.Repeat("a", 70*1024) + "\r\n\r\n"
	wantProxyError(t, e.raw(t, big), 431, "request head too large")
	fits := "GET / HTTP/1.1\r\nHost: " + e.host(5173) + "\r\nX-Big: " + strings.Repeat("a", 60*1024) + "\r\nConnection: close\r\n\r\n"
	if r := decodeReply(t, e.raw(t, fits)); r.Port != 5173 {
		t.Fatalf("60 KiB head: %+v", r)
	}
}

func (e *kernelEnv) testKernelRefusals(t *testing.T) {
	wantProxyError(t, e.get(t, e.host(4000), "/"), 502, "nothing listening on kernel port 4000")
	wantProxyError(t, e.get(t, e.host(5174), "/"), 502, "kernel port 5174: EACCES: reserved by the kernel")
}

func (e *kernelEnv) kernelOpens(t *testing.T) []int {
	t.Helper()
	var k struct {
		Opens []int `json:"opens"`
	}
	e.p.eval("linkTest.kernel()", &k)
	return k.Opens
}

func (e *kernelEnv) testKernel9222(t *testing.T) {
	wantProxyError(t, e.get(t, e.host(9222), "/json/version"), 502, "kernel port 9222: EACCES")
	if opens := e.kernelOpens(t); slices.Contains(opens, 9222) || !slices.Contains(opens, 4000) {
		t.Fatalf("page saw OPENs for %v", opens)
	}
}

func (e *kernelEnv) testKernelTimeout(t *testing.T) {
	start := time.Now()
	wantProxyError(t, e.get(t, e.host(5175), "/"), 504, "kernel port 5175 did not answer")
	if d := time.Since(start); d < 9*time.Second || d > 20*time.Second {
		t.Fatalf("504 after %v", d)
	}
}

func (e *kernelEnv) testKernelNoPage(t *testing.T) {
	e.p.eval("linkTest.drop()", nil)
	e.a.waitStderr(t, "link closed by seven", 10*time.Second)
	wantProxyError(t, e.get(t, e.host(5173), "/"), 502, "no seven page connected")
}

func (b *chromeProc) openTarget(t *testing.T, url string) *page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &res); err != nil {
		t.Fatal(err)
	}
	s, err := b.client.Attach(ctx, res.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = b.client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
	})
	return &page{t: t, b: b, target: res.TargetID, session: s}
}

func TestKernelBindFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port := hostPort(t, ln.Addr().String())
	b := launchBrowser(t)
	srv := pageServer(t)
	a := startAttach(t, false, "--cdp", b.endpoint, "--origin", srv.URL, "--kernel-port", strconv.Itoa(port))
	e := &linkEnv{t: t, b: b, a: a, origin: srv.URL}
	e.echo = serveTCP(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	e.p = b.openPage(t, srv.URL+"/index.html?kernel")
	e.waitUp("")
	a.waitStderr(t, fmt.Sprintf("kernel services are off: cannot listen on 127.0.0.1:%d (", port), 10*time.Second)
	a.waitStderr(t, "; try --kernel-port", time.Second)
	if r := e.echoTest(5000, false); !r.OK {
		t.Fatalf("attach stopped serving after the bind failure: %+v", r)
	}
	if a.cmd.ProcessState != nil {
		t.Fatal("attach exited")
	}
}

func TestKernelOff(t *testing.T) {
	for _, c := range []struct {
		name  string
		query string
		flags []string
	}{
		{"--no-kernel", "?kernel", []string{"--no-kernel"}},
		{"page without kernel-in", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := launchBrowser(t)
			srv := pageServer(t)
			port := freePort(t)
			args := append([]string{"--cdp", b.endpoint, "--origin", srv.URL, "--kernel-port", strconv.Itoa(port)}, c.flags...)
			a := startAttach(t, false, args...)
			e := &linkEnv{t: t, b: b, a: a, origin: srv.URL}
			e.p = b.openPage(t, srv.URL+"/index.html"+c.query)
			e.waitUp("")
			time.Sleep(500 * time.Millisecond)
			if strings.Contains(a.stderr.String(), "kernel services") {
				t.Fatalf("stderr mentions kernel services:\n%s", a.stderr.String())
			}
			if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
				_ = conn.Close()
				t.Fatalf("something listens on the kernel port %d", port)
			}
		})
	}
}

func TestKernelPortFlag(t *testing.T) {
	for _, v := range []string{"0", "65536", "x"} {
		_, errOut, code := slicc(t, "attach", "--signal", "stdio", "--kernel-port", v)
		if code != 2 || !strings.Contains(errOut, "kernel-port") {
			t.Fatalf("--kernel-port %s: code %d, stderr %q", v, code, errOut)
		}
	}
	out, _, code := slicc(t, "attach", "--help")
	if code != 0 || !strings.Contains(out, "--kernel-port <n>") || !strings.Contains(out, "--no-kernel") {
		t.Fatalf("help: %q", out)
	}
}
