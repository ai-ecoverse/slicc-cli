package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var pairingURL = regexp.MustCompile(`#slicc-pair=(\d+)\.([A-Za-z0-9_-]{22})\.([0-9a-f]{64})$`)

type loopProc struct {
	cmd    *exec.Cmd
	stderr *syncBuffer
	stdin  io.WriteCloser
	urls   chan string
	lines  *syncBuffer
}

type loopEnv struct {
	env    []string
	config string
	first  int
	last   int
}

func loopbackRange(t *testing.T) (int, int) {
	t.Helper()
	for try := 0; try < 50; try++ {
		_, base := hostPort(t, closedAddr(t))
		if base+9 > 65535 {
			continue
		}
		ok := true
		for p := base; p <= base+9 && ok; p++ {
			ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(p))
			if err != nil {
				ok = false
				continue
			}
			_ = ln.Close()
		}
		if ok {
			return base, base + 9
		}
	}
	t.Fatal("no 10 free ports in a row")
	return 0, 0
}

func newLoopEnv(t *testing.T) *loopEnv {
	t.Helper()
	root := t.TempDir()
	set := map[string]string{
		"HOME":            root,
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"APPDATA":         filepath.Join(root, "Roaming"),
	}
	for _, d := range set {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := set["XDG_CONFIG_HOME"]
	switch runtime.GOOS {
	case "darwin":
		config = filepath.Join(root, "Library", "Application Support")
	case "windows":
		config = set["APPDATA"]
	}
	first, last := loopbackRange(t)
	set["SLICC_LOOPBACK_PORTS"] = fmt.Sprintf("%d-%d", first, last)
	set["SLICC_DEBUG"] = "1"
	return &loopEnv{env: overrideEnv(os.Environ(), set), config: filepath.Join(config, "slicc"), first: first, last: last}
}

func (e *loopEnv) ports() string { return fmt.Sprintf("%d-%d", e.first, e.last) }

func startLoop(t *testing.T, e *loopEnv, verb string, args ...string) *loopProc {
	t.Helper()
	var argv []string
	if verb != "" {
		argv = append(argv, verb)
	}
	cmd := exec.Command(binary, append(argv, args...)...)
	cmd.Env = e.env
	o := &loopProc{cmd: cmd, stderr: &syncBuffer{}, urls: make(chan string, 16), lines: &syncBuffer{}}
	cmd.Stderr = o.stderr
	var err error
	if o.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			_, _ = o.lines.Write([]byte(sc.Text() + "\n"))
			if pairingURL.MatchString(sc.Text()) {
				o.urls <- sc.Text()
			}
		}
	}()
	t.Cleanup(func() {
		o.stop(t)
		if t.Failed() {
			t.Logf("slicc %s stderr:\n%s", verb, o.stderr.String())
		}
	})
	return o
}

func (o *loopProc) stop(t *testing.T) {
	t.Helper()
	if o.cmd.ProcessState == nil {
		reap(t, o.cmd, "slicc loopback")
	}
}

func (o *loopProc) url(t *testing.T) string {
	t.Helper()
	select {
	case u := <-o.urls:
		return u
	case <-time.After(20 * time.Second):
		t.Fatalf("no pairing URL on stdout; stderr:\n%s", o.stderr.String())
		return ""
	}
}

func (o *loopProc) newURL(t *testing.T) string {
	t.Helper()
	if _, err := io.WriteString(o.stdin, "\n"); err != nil {
		t.Fatal(err)
	}
	return o.url(t)
}

func (o *loopProc) waitStderr(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(o.stderr.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("stderr never said %q:\n%s", want, o.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type pairing struct {
	port        int
	token       string
	fingerprint string
}

func parsePairing(t *testing.T, u string) pairing {
	t.Helper()
	m := pairingURL.FindStringSubmatch(u)
	if m == nil {
		t.Fatalf("not a pairing URL: %q", u)
	}
	port, _ := strconv.Atoi(m[1])
	return pairing{port: port, token: m[2], fingerprint: m[3]}
}

func colons(fp string) string {
	var parts []string
	for i := 0; i+2 <= len(fp); i += 2 {
		parts = append(parts, strings.ToUpper(fp[i:i+2]))
	}
	return strings.Join(parts, ":")
}

type loopResult struct {
	Linked      bool   `json:"linked"`
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"`
	Node        string `json:"node"`
	Name        string `json:"name"`
	Error       string `json:"error"`
	Code        string `json:"code"`
	Status      int    `json:"status"`
	Attempts    int    `json:"attempts"`
}

type loopHello struct {
	Mode   string   `json:"mode"`
	Name   string   `json:"name"`
	Host   string   `json:"host"`
	Node   string   `json:"node"`
	Caps   []string `json:"caps"`
	Policy struct {
		Deny []string `json:"deny"`
	} `json:"policy"`
}

func confirm(t *testing.T, p *page, pending string) loopResult {
	t.Helper()
	var r loopResult
	p.eval("linkTest.loopback.confirm("+pending+")", &r)
	return r
}

func (p *page) echoOK(t *testing.T, addr string, size int) {
	t.Helper()
	host, port := hostPort(t, addr)
	var r echoResult
	p.eval("linkTest.echo("+jsArgs(host, port, size)+")", &r)
	if !r.OK || r.Bytes != size {
		t.Fatalf("echo over the loopback link: %+v", r)
	}
}

func loopEcho(t *testing.T) string {
	t.Helper()
	return serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
	})
}

func writeLoopActivePort(t *testing.T, e *loopEnv) int {
	t.Helper()
	port := freePort(t)
	dir := filepath.Join(e.config, "seven-profiles", "fake")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/fake\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestLoopbackLinksSevenWithoutCDP(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	e := newLoopEnv(t)
	devPort := writeLoopActivePort(t, e)
	home := envMap(e.env)["HOME"]
	e.env = overrideEnv(withDiscovery(e.env, "home"), map[string]string{"USERPROFILE": home, "LOCALAPPDATA": filepath.Join(home, "Local")})
	mount := t.TempDir()
	if err := os.WriteFile(filepath.Join(mount, "hello.txt"), []byte("hello loopback"), 0o600); err != nil {
		t.Fatal(err)
	}
	kport := freePort(t)
	pageURL := srv.URL + "/index.html?kernel&ports=" + e.ports()
	args := []string{"--page", pageURL, "--name", "it-loop", "--kernel-port", strconv.Itoa(kport), "--mount", mount + ":proj"}
	o := startLoop(t, e, "pair", args...)
	u := o.url(t)
	pr := parsePairing(t, u)
	if !strings.HasPrefix(u, pageURL+"#slicc-pair=") || pr.port < e.first || pr.port > e.last {
		t.Fatalf("pairing URL %q, range %s", u, e.ports())
	}
	o.waitStderr(t, "fingerprint sha-256 "+colons(pr.fingerprint))
	o.waitStderr(t, fmt.Sprintf("listening on 127.0.0.1:%d", pr.port))

	p := b.openPage(t, u)
	var pending pairing
	var raw map[string]any
	p.eval("linkTest.loopback.pending()", &raw)
	pending.port, pending.token, pending.fingerprint = int(raw["port"].(float64)), raw["token"].(string), raw["fingerprint"].(string)
	var hash string
	p.eval("location.hash", &hash)
	if pending != pr || hash != "" {
		t.Fatalf("the page read %+v from the fragment and left %q, want %+v and none", pending, hash, pr)
	}
	r := confirm(t, p, "")
	if !r.Linked || r.Name != "it-loop" || r.Port != pr.port || r.Fingerprint != pr.fingerprint || r.Node == "" {
		t.Fatalf("pairing: %+v", r)
	}
	var h loopHello
	p.eval("linkTest.ready()", &h)
	echo := loopEcho(t)

	t.Run("hello offers no cdp and protects the debugging ports", func(t *testing.T) {
		if h.Mode != "local" || h.Name != "it-loop" || h.Node != r.Node {
			t.Fatalf("hello %+v", h)
		}
		for _, c := range []string{"net", "http", "credentialed", "ws", "fs", "unlink-loopback"} {
			if !slices.Contains(h.Caps, c) {
				t.Fatalf("caps %v lack %s", h.Caps, c)
			}
		}
		if slices.Contains(h.Caps, "cdp") {
			t.Fatalf("caps %v offer cdp", h.Caps)
		}
		for _, port := range []int{9222, 9229, pr.port, kport, devPort} {
			if want := fmt.Sprintf("127.0.0.1/32:%d", port); !slices.Contains(h.Policy.Deny, want) {
				t.Fatalf("policy deny %v lacks %s", h.Policy.Deny, want)
			}
		}
		late := filepath.Join(e.config, "seven-profiles", "late")
		latePort := freePort(t)
		if err := os.MkdirAll(late, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(late, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/late\n", latePort)), 0o600); err != nil {
			t.Fatal(err)
		}
		o.waitStderr(t, fmt.Sprintf("denied new DevToolsActivePort port %d on every address of this machine", latePort))
		hosts := []string{"127.0.0.1", "127.0.0.2"}
		if lan, ok := findLANAddress(); ok {
			hosts = append(hosts, lan.String())
		}
		for _, port := range []int{pr.port, devPort, latePort} {
			for _, host := range hosts {
				if runtime.GOOS != "linux" && host == "127.0.0.2" {
					continue
				}
				var res echoResult
				p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": host, "port": port})+")", &res)
				if res.Reset != "EACCES" {
					t.Fatalf("tcp to protected port %s:%d: %+v", host, port, res)
				}
			}
		}
	})

	t.Run("net, http, fs and kernel streams", func(t *testing.T) {
		p.echoOK(t, echo, 300000)
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello over http") }))
		t.Cleanup(up.Close)
		var hr httpResult
		p.eval("linkTest.http("+jsArgs(map[string]any{"url": up.URL + "/x", "method": "GET", "headers": [][2]string{}})+")", &hr)
		if hr.Status != 200 || hr.body(t) != "hello over http" {
			t.Fatalf("http: %+v", hr)
		}
		var st struct {
			Kind string `json:"kind"`
			Size int    `json:"size"`
		}
		p.eval("linkTest.fs.call('proj', 'stat', { path: 'hello.txt' })", &st)
		if st.Kind != "file" || st.Size != len("hello loopback") {
			t.Fatalf("fs stat: %+v", st)
		}
		o.waitStderr(t, fmt.Sprintf("kernel services on http://<port>.kernel.localhost:%d/", kport))
		k := &kernelEnv{port: kport, addr: fmt.Sprintf("127.0.0.1:%d", kport)}
		if kr := decodeReply(t, k.get(t, k.host(5173), "/via-loopback")); kr.Port != 5173 || kr.Path != "/via-loopback" {
			t.Fatalf("kernel: %+v", kr)
		}
	})

	t.Run("the pin is on disk, mode 0600", func(t *testing.T) {
		var pageFP string
		p.eval("linkTest.loopback.fingerprint()", &pageFP)
		path := filepath.Join(e.config, "loopback-peers.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			Version int `json:"version"`
			Peers   map[string]struct {
				Origin    string `json:"origin"`
				FirstSeen string `json:"firstSeen"`
			} `json:"peers"`
		}
		if json.Unmarshal(data, &f) != nil || f.Version != 1 || f.Peers[pageFP].Origin != srv.URL || f.Peers[pageFP].FirstSeen == "" {
			t.Fatalf("%s: %s (page %s)", path, data, pageFP)
		}
		for _, name := range []string{"loopback-peers.json", "loopback-identity.json"} {
			fi, err := os.Stat(filepath.Join(e.config, name))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode %v", name, fi.Mode().Perm())
			}
		}
	})

	t.Run("a used token is refused", func(t *testing.T) {
		res := confirm(t, p, jsArgs(map[string]any{"port": pr.port, "token": pr.token, "fingerprint": pr.fingerprint}))
		if res.Linked || res.Status != 403 {
			t.Fatalf("second use of the token: %+v", res)
		}
		p.echoOK(t, echo, 1000)
	})

	t.Run("restart: same certificate and port, the page signals again without a token", func(t *testing.T) {
		o.stop(t)
		o2 := startLoop(t, e, "pair", args...)
		pr2 := parsePairing(t, o2.url(t))
		if pr2.port != pr.port || pr2.fingerprint != pr.fingerprint || pr2.token == pr.token {
			t.Fatalf("after a restart %+v, before %+v", pr2, pr)
		}
		o2.waitStderr(t, "this computer: it-loop")
		var res loopResult
		p.eval("linkTest.loopback.reconnect()", &res)
		if !res.Linked || res.Port != pr.port || res.Node == r.Node {
			t.Fatalf("re-signal after a restart: %+v (first node %s)", res, r.Node)
		}
		p.eval("linkTest.ready()", &h)
		p.echoOK(t, echo, 200000)

		p.reload()
		if res = loopSignal(t, p); !res.Linked {
			t.Fatalf("re-signal after a reload, with the certificate from IndexedDB: %+v", res)
		}
		p.eval("linkTest.ready()", &h)
		p.echoOK(t, echo, 1000)
		if strings.Contains(o2.stderr.String(), "paired with") {
			t.Fatalf("the restarted node paired again:\n%s", o2.stderr.String())
		}
	})
}

type gateAnswer struct {
	status int
	body   string
	header http.Header
}

func gateDo(t *testing.T, port int, method, path, host string, header map[string]string, body []byte) gateAnswer {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", method, path, host)
	for k, v := range header {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	if body != nil {
		fmt.Fprintf(&b, "Content-Type: application/json\r\nContent-Length: %d\r\n", len(body))
	}
	b.WriteString("\r\n")
	b.Write(body)
	if _, err := c.Write(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return gateAnswer{status: resp.StatusCode, body: string(got), header: resp.Header}
}

func wantForbidden(t *testing.T, what string, a gateAnswer, acao string) {
	t.Helper()
	if a.status != 403 || a.body != "" || a.header.Get("Access-Control-Allow-Origin") != acao || a.header.Get("Access-Control-Allow-Private-Network") != "" {
		t.Fatalf("%s: %d %q %v, want 403 with no body and Access-Control-Allow-Origin %q", what, a.status, a.body, a.header, acao)
	}
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoopbackGateRefuses(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	other := pageServer(t)
	e := newLoopEnv(t)
	o := startLoop(t, e, "pair", "--page", srv.URL+"/index.html?ports="+e.ports(), "--origin", other.URL, "--origin", "http://127.0.0.1:1")
	pr := parsePairing(t, o.url(t))
	host := fmt.Sprintf("127.0.0.1:%d", pr.port)
	p := b.openPage(t, srv.URL+"/index.html?ports="+e.ports())
	var offer string
	p.eval("linkTest.loopback.offerSdp()", &offer)
	origin := map[string]string{"Origin": srv.URL}
	pairBody := func(token string) []byte { return jsonBody(t, map[string]string{"token": token, "offer": offer}) }

	wantForbidden(t, "a wrong token", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, pairBody("AAAAAAAAAAAAAAAAAAAAAA")), srv.URL)
	o.waitStderr(t, "refused a pairing request from "+srv.URL+": the token is wrong, used or expired")
	wantForbidden(t, "an unpaired fingerprint", gateDo(t, pr.port, "POST", "/slicc/v1/signal", host, origin, jsonBody(t, map[string]string{"offer": offer})), srv.URL)

	for _, bad := range []string{"https://www.sliccy.ai", "https://vault.sliccy.ai", "https://mcpcat.sliccy.ai", "https://get.sliccy.ai", "https://vault-main.sliccy.ai", "https://example.com", "https://seven.sliccy.ai.example.com", "null", "http://127.0.0.1:2"} {
		t.Run("origin "+bad, func(t *testing.T) {
			h := map[string]string{"Origin": bad, "Access-Control-Request-Method": "POST"}
			wantForbidden(t, "preflight from "+bad, gateDo(t, pr.port, "OPTIONS", "/slicc/v1/signal", host, h, nil), "")
			wantForbidden(t, "origin "+bad, gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, h, pairBody("AAAAAAAAAAAAAAAAAAAAAA")), "")
		})
	}
	wantForbidden(t, "no Origin", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, nil, pairBody(pr.token)), "")

	for _, okHost := range []string{host, fmt.Sprintf("localhost:%d", pr.port), fmt.Sprintf("[::1]:%d", pr.port)} {
		a := gateDo(t, pr.port, "OPTIONS", "/slicc/v1/pair", okHost, map[string]string{"Origin": srv.URL, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type", "Access-Control-Request-Private-Network": "true"}, nil)
		if a.status != 204 || a.header.Get("Access-Control-Allow-Origin") != srv.URL || a.header.Get("Access-Control-Allow-Private-Network") != "true" || a.header.Get("Vary") != "Origin" || a.header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("preflight with Host %s: %d %v", okHost, a.status, a.header)
		}
	}
	wantForbidden(t, "preflight for GET", gateDo(t, pr.port, "OPTIONS", "/slicc/v1/pair", host, map[string]string{"Origin": srv.URL, "Access-Control-Request-Method": "GET"}, nil), "")
	wantForbidden(t, "preflight with another header", gateDo(t, pr.port, "OPTIONS", "/slicc/v1/pair", host, map[string]string{"Origin": srv.URL, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type, x-evil"}, nil), "")

	for _, badHost := range []string{"evil.example:" + strconv.Itoa(pr.port), "seven.sliccy.ai", fmt.Sprintf("127.0.0.1:%d", pr.port+1), "127.0.0.1", fmt.Sprintf("127.0.0.2:%d", pr.port)} {
		wantForbidden(t, "Host "+badHost, gateDo(t, pr.port, "POST", "/slicc/v1/pair", badHost, origin, pairBody(pr.token)), "")
	}
	for _, path := range []string{"/", "/slicc/v1/pair/", "/slicc/v1/pair?x=1", "/slicc/v1/PAIR", "/slicc/v1/other", "/slicc/v1//pair", "http://" + host + "/slicc/v1/pair"} {
		wantForbidden(t, "path "+path, gateDo(t, pr.port, "POST", path, host, origin, pairBody(pr.token)), "")
	}
	for _, method := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		wantForbidden(t, "method "+method, gateDo(t, pr.port, method, "/slicc/v1/pair", host, origin, pairBody(pr.token)), srv.URL)
	}
	big := jsonBody(t, map[string]string{"token": pr.token, "offer": offer + strings.Repeat("x", 64<<10)})
	wantForbidden(t, "a body over 64 KiB", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, big), srv.URL)
	wantForbidden(t, "a body that isn't a JSON object", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, []byte(`["`+pr.token+`"]`)), srv.URL)
	wantForbidden(t, "an offer without a fingerprint", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, jsonBody(t, map[string]string{"token": pr.token, "offer": "v=0\r\n"})), srv.URL)

	r := confirm(t, p, jsArgs(map[string]any{"port": pr.port, "token": pr.token, "fingerprint": pr.fingerprint}))
	if !r.Linked {
		t.Fatalf("the token was burned by a refused request: %+v", r)
	}
	var h loopHello
	p.eval("linkTest.ready()", &h)

	t.Run("a paired fingerprint from another origin", func(t *testing.T) {
		wantForbidden(t, "signal from another origin", gateDo(t, pr.port, "POST", "/slicc/v1/signal", host, map[string]string{"Origin": "http://127.0.0.1:1"}, jsonBody(t, map[string]string{"offer": offer})), "http://127.0.0.1:1")
	})

	t.Run("an unpaired page in the browser", func(t *testing.T) {
		q := b.openPage(t, other.URL+"/index.html?ports="+e.ports())
		var sdp string
		q.eval("linkTest.loopback.offerSdp()", &sdp)
		var res loopResult
		q.eval("linkTest.loopback.post("+jsArgs(pr.port, "/slicc/v1/signal", map[string]string{"offer": sdp})+")", &res)
		if res.Status != 403 || res.Code != "refused" {
			t.Fatalf("signal from an unpaired page: %+v", res)
		}
	})

	t.Run("a squatter's fingerprint makes the page refuse", func(t *testing.T) {
		fresh := parsePairing(t, o.newURL(t))
		wrong := fresh.fingerprint[:63] + string("0123456789abcdef"[(strings.IndexByte("0123456789abcdef", fresh.fingerprint[63])+1)%16])
		res := confirm(t, p, jsArgs(map[string]any{"port": fresh.port, "token": fresh.token, "fingerprint": wrong}))
		if res.Linked || res.Code != "fingerprint" {
			t.Fatalf("an answer whose fingerprint isn't the URL's: %+v", res)
		}
		var again loopResult
		p.eval("linkTest.loopback.signal()", &again)
		if !again.Linked || again.Fingerprint != pr.fingerprint {
			t.Fatalf("signal after the refusal: %+v", again)
		}
	})
}

func TestLoopbackRateLimitsPairing(t *testing.T) {
	srv := pageServer(t)
	e := newLoopEnv(t)
	o := startLoop(t, e, "pair", "--page", srv.URL+"/index.html")
	pr := parsePairing(t, o.url(t))
	host := fmt.Sprintf("127.0.0.1:%d", pr.port)
	offer := "v=0\r\na=fingerprint:sha-256 " + colons(strings.Repeat("ab", 32)) + "\r\n"
	origin := map[string]string{"Origin": srv.URL}
	for i := 0; i < 10; i++ {
		wantForbidden(t, "a wrong token", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, jsonBody(t, map[string]string{"token": fmt.Sprintf("wrong%017d", i), "offer": offer})), srv.URL)
	}
	wantForbidden(t, "the right token past the limit", gateDo(t, pr.port, "POST", "/slicc/v1/pair", host, origin, jsonBody(t, map[string]string{"token": pr.token, "offer": offer})), srv.URL)
	o.waitStderr(t, "refused a pairing: more than 10 a minute")
	if n := strings.Count(o.stderr.String(), "the token is wrong, used or expired"); n != 10 {
		t.Fatalf("%d wrong-token refusals, want 10:\n%s", n, o.stderr.String())
	}
	if strings.Contains(o.stderr.String(), "paired with") {
		t.Fatal("paired past the rate limit")
	}
}

func TestLoopbackSeveralSevensAndUnpair(t *testing.T) {
	b := launchBrowser(t)
	one := pageServer(t)
	two := pageServer(t)
	e := newLoopEnv(t)
	o := startLoop(t, e, "pair", "--page", one.URL+"/index.html?ports="+e.ports(), "--origin", two.URL)
	p1 := b.openPage(t, o.url(t))
	if r := confirm(t, p1, ""); !r.Linked {
		t.Fatalf("first seven: %+v", r)
	}
	u2 := strings.Replace(o.newURL(t), one.URL, two.URL, 1)
	p2 := b.openPage(t, u2)
	if r := confirm(t, p2, ""); !r.Linked {
		t.Fatalf("second seven: %+v", r)
	}
	var h loopHello
	p1.eval("linkTest.ready()", &h)
	p2.eval("linkTest.ready()", &h)
	echo := loopEcho(t)
	p1.echoOK(t, echo, 50000)
	p2.echoOK(t, echo, 50000)
	var fp1, fp2 string
	p1.eval("linkTest.loopback.fingerprint()", &fp1)
	p2.eval("linkTest.loopback.fingerprint()", &fp2)
	if fp1 == fp2 {
		t.Fatal("two origins share a certificate")
	}

	run := func(args ...string) (string, int) {
		cmd := exec.Command(binary, append([]string{"unpair"}, args...)...)
		cmd.Env = e.env
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}
	out, code := run()
	if code != 0 || !strings.Contains(out, fp1+"  "+one.URL) || !strings.Contains(out, fp2+"  "+two.URL) {
		t.Fatalf("unpair lists: %d %q", code, out)
	}
	var closedRes map[string]any
	p2.eval("linkTest.loopback.close()", &closedRes)
	if out, code := run(fp2[:12]); code != 0 || !strings.Contains(out, "forgot "+fp2[:4]) {
		t.Fatalf("unpair %s: %d %q", fp2[:12], code, out)
	}
	o.waitStderr(t, "loopback: released "+fp2[:4])
	if _, code := run("ffff" + fp2[:12]); code != 1 {
		t.Fatalf("unpair of an unknown fingerprint exits %d", code)
	}
	var res loopResult
	res = loopSignal(t, p2)
	if res.Linked || res.Status != 403 {
		t.Fatalf("signal after unpair: %+v", res)
	}
	res = loopSignal(t, p1)
	if !res.Linked {
		t.Fatalf("the other seven after unpair: %+v", res)
	}
	var ul struct {
		Sent   bool   `json:"sent"`
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}
	p1.eval("linkTest.loopback.unlink()", &ul)
	if !ul.Sent || ul.Reason != "closed" {
		t.Fatalf("unlink in-band: %+v", ul)
	}
	o.waitStderr(t, "unlinked by seven on "+one.URL+" ("+fp1[:4])
	o.waitStderr(t, "loopback: released "+fp1[:4])
	if out, _ := run(); out != "no paired sevens\n" {
		t.Fatalf("unpair after the in-band unlink: %q", out)
	}
	res = loopSignal(t, p1)
	if res.Linked || res.Error != "not paired" {
		t.Fatalf("signal after the page unlinked: %+v", res)
	}
	if r := confirm(t, p1, jsArgs(pendingFrom(t, o.newURL(t)))); !r.Linked {
		t.Fatalf("pairing again after the unlink: %+v", r)
	}
	if out, code := run("--all"); code != 0 || out != "forgot 1 paired sevens\n" {
		t.Fatalf("unpair --all: %d %q", code, out)
	}
	res = loopSignal(t, p1)
	if res.Linked || res.Status != 403 {
		t.Fatalf("signal after unpair --all: %+v", res)
	}
	if out, _ := run(); out != "no paired sevens\n" {
		t.Fatalf("unpair after --all: %q", out)
	}
}

func loopSignal(t *testing.T, p *page) loopResult {
	t.Helper()
	var r loopResult
	p.eval("linkTest.loopback.signal()", &r)
	return r
}

func pendingFrom(t *testing.T, u string) map[string]any {
	t.Helper()
	pr := parsePairing(t, u)
	return map[string]any{"port": pr.port, "token": pr.token, "fingerprint": pr.fingerprint}
}

func TestLoopbackOpenUsesTheDefaultBrowserHook(t *testing.T) {
	if os.Getenv("SLICC_OPEN_COMMAND") == "" {
		t.Fatal("the harness doesn't set SLICC_OPEN_COMMAND, so slicc open could open a real browser")
	}
	tool := filepath.Join(t.TempDir(), exeName("fakeopen"))
	if out, err := exec.Command("go", "build", "-o", tool, "./testdata/fakeopen").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, argv := range [][]string{{"open"}, {"--default-browser"}, {"pair"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			e := newLoopEnv(t)
			out := filepath.Join(t.TempDir(), "opened")
			e.env = overrideEnv(e.env, map[string]string{"SLICC_OPEN_COMMAND": tool, "SLICC_FAKEOPEN_OUT": out})
			o := startLoop(t, e, argv[0], "--page", "https://seven.sliccy.ai/")
			u := o.url(t)
			if !strings.HasPrefix(u, "https://seven.sliccy.ai/#slicc-pair=") {
				t.Fatalf("pairing URL %q", u)
			}
			if argv[0] == "pair" {
				time.Sleep(time.Second)
				if _, err := os.Stat(out); err == nil {
					t.Fatal("slicc pair opened a browser")
				}
				return
			}
			o.waitStderr(t, "opened the pairing URL in the default browser")
			got, _ := os.ReadFile(out)
			if string(got) != u+"\n" {
				t.Fatalf("the opener got %q, stdout had %q", got, u)
			}
		})
	}
}

func TestLoopbackFirstRunsShareOneIdentity(t *testing.T) {
	e := newLoopEnv(t)
	a := startLoop(t, e, "pair", "--page", "https://seven.sliccy.ai/")
	b := startLoop(t, e, "pair", "--page", "https://seven.sliccy.ai/")
	pa, pb := parsePairing(t, a.url(t)), parsePairing(t, b.url(t))
	if pa.fingerprint != pb.fingerprint || pa.port == pb.port {
		t.Fatalf("two first runs: %+v and %+v", pa, pb)
	}
	a.stop(t)
	b.stop(t)
	c := startLoop(t, e, "pair", "--page", "https://seven.sliccy.ai/")
	if pc := parsePairing(t, c.url(t)); pc.fingerprint != pa.fingerprint {
		t.Fatalf("after a restart %s, before %s", pc.fingerprint, pa.fingerprint)
	}
}

func TestDefaultBrowserFlagFollowsComputeArgv(t *testing.T) {
	e := newLoopEnv(t)
	o := startLoop(t, e, "", "--runner", "sh", "-c", "--", "--default-browser", "--page", "https://seven.sliccy.ai/")
	if u := o.url(t); !strings.HasPrefix(u, "https://seven.sliccy.ai/#slicc-pair=") {
		t.Fatalf("pairing URL %q", u)
	}
	o.waitStderr(t, "ssh: runner sh -c")
	for _, args := range [][]string{{"--default-browser=true"}, {"--default-browser=false", "--default-browser=true"}, {"--default-browser", "-default-browser"}} {
		o2 := startLoop(t, e, "", append(args, "--page", "https://seven.sliccy.ai/")...)
		if u := o2.url(t); !pairingURL.MatchString(u) {
			t.Fatalf("%v: %q", args, u)
		}
		o2.stop(t)
	}
	if out, errOut, code := runWithEnv(t, e.env, "--default-browser", "--default-browser=false", "--no-fallback"); code != 1 || !strings.Contains(errOut, "no browser to control") || pairingURL.MatchString(out) {
		t.Fatalf("--default-browser then =false: %d %q %q", code, out, errOut)
	}
	for _, args := range [][]string{{"--no-fallback", "--", "--default-browser"}, {"--no-fallback", "--default-browser=nope"}} {
		out, errOut, code := runWithEnv(t, e.env, args...)
		if code != 2 || pairingURL.MatchString(out) {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	if out, errOut, code := runWithEnv(t, e.env, "--default-browser=false", "--no-fallback"); code != 1 || !strings.Contains(errOut, "no browser to control") || pairingURL.MatchString(out) {
		t.Fatalf("--default-browser=false: %d %q %q", code, out, errOut)
	}
	cmd := exec.Command(binary, "--runner", "echo", "--default-browser", "--", "--yes", "--no-fallback")
	cmd.Env = e.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 || pairingURL.MatchString(stdout.String()) || !strings.Contains(stderr.String(), "no browser to control") {
		t.Fatalf("--default-browser inside the runner argv: %d %q %q", code, stdout.String(), stderr.String())
	}
}

func TestLoopbackHelpAndUsage(t *testing.T) {
	for _, argv := range [][]string{{"open", "--help"}, {"pair", "--help"}, {"--default-browser", "--help"}} {
		out, _, code := slicc(t, argv...)
		if code != 0 || !strings.Contains(out, "usage: slicc open [flags]") || !strings.Contains(out, "17117") {
			t.Fatalf("%v: %d %q", argv, code, out)
		}
	}
	if out, _, code := slicc(t, "unpair", "--help"); code != 0 || !strings.Contains(out, "usage: slicc unpair") {
		t.Fatalf("unpair --help: %d %q", code, out)
	}
	if out, _, _ := slicc(t, "--help"); !strings.Contains(out, "slicc open") || !strings.Contains(out, "slicc pair") || !strings.Contains(out, "--default-browser") {
		t.Fatalf("--help: %q", out)
	}
	if _, errOut, code := slicc(t, "open", "--browser", "chrome"); code != 2 || !strings.Contains(errOut, "-browser") {
		t.Fatalf("open --browser: %d %q", code, errOut)
	}
	if _, _, code := slicc(t, "unpair", "a", "b"); code != 2 {
		t.Fatalf("unpair a b: %d", code)
	}
}

func TestTelemetryNamesTheLoopbackCommands(t *testing.T) {
	gh := newFakeGitHub(t, "v1.0.0", nil)
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	if r := runBin(t, gh.env(t, col.env()...), bin, "unpair"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if enter := col.find("enter"); enter == nil || enter.Source != "unpair" {
		t.Fatalf("beacons: %+v", col.got())
	}
}

func TestLoopbackWithNoFreePortExitsOne(t *testing.T) {
	e := newLoopEnv(t)
	var held []net.Listener
	for p := e.first; p <= e.first+1; p++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, ln)
	}
	defer func() {
		for _, ln := range held {
			_ = ln.Close()
		}
	}()
	env := overrideEnv(e.env, map[string]string{"SLICC_LOOPBACK_PORTS": fmt.Sprintf("%d-%d", e.first, e.first+1)})
	cmd := exec.Command(binary, "pair")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	want := fmt.Sprintf("slicc: no free port in %d-%d on 127.0.0.1\n", e.first, e.first+1)
	if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.HasSuffix(stderr.String(), want) || stdout.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestLoopbackPinsAreLockedAcrossProcesses(t *testing.T) {
	e := newLoopEnv(t)
	if err := os.MkdirAll(e.config, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(e.config, "loopback-peers.json.lock")
	unpair := func() (string, string, int) {
		cmd := exec.Command(binary, "unpair", "--all")
		cmd.Env = e.env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	release := holdFileLock(t, lock)
	if _, errOut, code := unpair(); code != 1 || !strings.Contains(errOut, "being changed by another slicc") {
		release()
		t.Fatalf("unpair while another process holds the lock: %d %q", code, errOut)
	}
	release()
	if out, errOut, code := unpair(); code != 0 || out != "forgot 0 paired sevens\n" {
		t.Fatalf("unpair once the lock is free, with its file left behind: %d %q %q", code, out, errOut)
	}
}

func TestLoopbackNewIdentityForgetsThePins(t *testing.T) {
	e := newLoopEnv(t)
	if err := os.MkdirAll(e.config, 0o700); err != nil {
		t.Fatal(err)
	}
	peers := filepath.Join(e.config, "loopback-peers.json")
	pin := fmt.Sprintf(`{"version":1,"peers":{%q:{"origin":"https://seven.sliccy.ai","firstSeen":"2026-10-13T00:00:00Z"}}}`, strings.Repeat("ab", 32))
	if err := os.WriteFile(peers, []byte(pin), 0o600); err != nil {
		t.Fatal(err)
	}
	o := startLoop(t, e, "pair", "--page", "https://seven.sliccy.ai/")
	o.url(t)
	o.waitStderr(t, "new loopback identity for this computer")
	cmd := exec.Command(binary, "unpair")
	cmd.Env = e.env
	if out, _ := cmd.Output(); string(out) != "no paired sevens\n" {
		t.Fatalf("pins after a new identity: %q", out)
	}
}

func TestLoopbackLeavesUnreadableStateAlone(t *testing.T) {
	e := newLoopEnv(t)
	if err := os.MkdirAll(e.config, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(e.config, "loopback-identity.json")
	peers := filepath.Join(e.config, "loopback-peers.json")
	for _, path := range []string{identity, peers} {
		if err := os.WriteFile(path, []byte("{\"version\": 99}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(binary, args...)
		cmd.Env = e.env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		return stdout.String() + stderr.String(), cmd.ProcessState.ExitCode()
	}
	if out, code := run("pair", "--page", "https://seven.sliccy.ai/"); code != 1 || !strings.Contains(out, "is not a loopback identity slicc can read") || pairingURL.MatchString(out) {
		t.Fatalf("pair with an unreadable identity: %d %q", code, out)
	}
	for _, args := range [][]string{{"unpair"}, {"unpair", "--all"}, {"unpair", "abcd"}} {
		if out, code := run(args...); code != 1 || !strings.Contains(out, "is not a list of paired sevens slicc can read") {
			t.Fatalf("%v with unreadable pins: %d %q", args, code, out)
		}
	}
	for _, path := range []string{identity, peers} {
		if got, _ := os.ReadFile(path); string(got) != "{\"version\": 99}\n" {
			t.Fatalf("%s was rewritten: %q", path, got)
		}
	}
}

func TestLoopbackKeepsTheKernelPortAndRunnerHelp(t *testing.T) {
	e := newLoopEnv(t)
	o := startLoop(t, e, "pair", "--page", "https://seven.sliccy.ai/", "--kernel-port", strconv.Itoa(e.first), "--runner", "sh", "--help")
	pr := parsePairing(t, o.url(t))
	if pr.port == e.first {
		t.Fatalf("the listener took the kernel port %d", e.first)
	}
	o.waitStderr(t, "ssh: runner sh --help")
	if strings.Contains(o.lines.String(), "usage:") {
		t.Fatalf("--help inside the runner argv printed the usage:\n%s", o.lines.String())
	}
}
