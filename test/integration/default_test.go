package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

type flowEvent struct {
	Schema     int    `json:"schema"`
	Event      string `json:"event"`
	Browser    string `json:"browser"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Launched   bool   `json:"launched"`
	PID        int    `json:"pid"`
	CDP        string `json:"cdp"`
	ProfileDir string `json:"profileDir"`
	WSURL      string `json:"webSocketDebuggerUrl"`
	Tab        string `json:"tab"`
	Peer       string `json:"peer"`
	Caps       []string
	Policy     string   `json:"policy"`
	Reason     string   `json:"reason"`
	Message    string   `json:"message"`
	Browsers   []string `json:"browsers"`
	Entries    []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Endpoint *struct {
			SevenOpen bool `json:"sevenOpen"`
		} `json:"endpoint"`
	} `json:"entries"`
	raw string
}

type flowProc struct {
	cmd    *exec.Cmd
	events chan flowEvent
	stderr *syncBuffer
	done   chan struct{}
	code   int
	seen   []flowEvent
}

func startFlow(t *testing.T, env []string, args ...string) *flowProc {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	prepareInterruptible(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	f := &flowProc{cmd: cmd, events: make(chan flowEvent, 64), stderr: &syncBuffer{}, done: make(chan struct{})}
	cmd.Stderr = f.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			var e flowEvent
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				e = flowEvent{Event: "not-json", raw: sc.Text()}
			} else {
				e.raw = sc.Text()
			}
			f.events <- e
		}
		close(f.events)
		_ = cmd.Wait()
		f.code = cmd.ProcessState.ExitCode()
		close(f.done)
	}()
	t.Cleanup(func() {
		select {
		case <-f.done:
		default:
			_ = interrupt(cmd)
			select {
			case <-f.done:
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
		if t.Failed() {
			t.Logf("slicc stderr:\n%s", f.stderr.String())
		}
	})
	return f
}

func (f *flowProc) next(t *testing.T, want string) flowEvent {
	t.Helper()
	select {
	case e, ok := <-f.events:
		if !ok {
			t.Fatalf("stdout closed before %q; events %v", want, f.kinds())
		}
		f.seen = append(f.seen, e)
		if e.Schema != 1 {
			t.Fatalf("event without schema 1: %s", e.raw)
		}
		if e.Event != want {
			t.Fatalf("event %s, want %s; events %v", e.raw, want, f.kinds())
		}
		return e
	case <-time.After(90 * time.Second):
		t.Fatalf("no %q event; events %v", want, f.kinds())
	}
	return flowEvent{}
}

func (f *flowProc) kinds() []string {
	out := make([]string, len(f.seen))
	for i, e := range f.seen {
		out[i] = e.Event
	}
	return out
}

func (f *flowProc) wait(t *testing.T) int {
	t.Helper()
	for range f.events {
	}
	select {
	case <-f.done:
	case <-time.After(45 * time.Second):
		t.Fatalf("slicc did not exit; stderr %s", f.stderr.String())
	}
	return f.code
}

func (f *flowProc) waitStderr(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !strings.Contains(f.stderr.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("stderr never said %q:\n%s", want, f.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dialPage(t *testing.T, endpoint, url string) *page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := cdp.Dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var target string
	for deadline := time.Now().Add(15 * time.Second); target == ""; time.Sleep(100 * time.Millisecond) {
		targets, err := client.Targets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, tg := range targets {
			if tg.Type == "page" && tg.URL == url {
				target = tg.TargetID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tab at %s: %+v", url, targets)
		}
	}
	s, err := client.Attach(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	p := &page{t: t, b: &chromeProc{endpoint: endpoint, client: client}, target: target, session: s}
	p.waitLoaded(url, 15*time.Second)
	return p
}

func countTabs(t *testing.T, client *cdp.Client, url string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targets, err := client.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tg := range targets {
		if tg.Type == "page" && tg.URL == url {
			n++
		}
	}
	return n
}

func echoOverLink(t *testing.T, p *page) {
	t.Helper()
	echo := serveTCP(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	host, port := hostPort(t, echo)
	p.waitFor("linkTest.status().up", 30*time.Second)
	var res struct {
		OK    bool   `json:"ok"`
		Reset string `json:"reset"`
		Error string `json:"error"`
	}
	p.eval("linkTest.echo("+jsArgs(host, port, 100000)+")", &res)
	if !res.OK {
		t.Fatalf("echo over the link: %+v", res)
	}
}

func TestDefaultJSONLaunchesLinksAndCloses(t *testing.T) {
	env, _ := launchEnv(t)
	profile := "it-default-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	env, id := testChrome(t, env)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	f := startFlow(t, env, "--json", "--browser", id, "--profile", profile, "--headless", "--page", pageURL, "--name", "it-default")

	d := f.next(t, "discovered")
	if len(d.Entries) == 0 || !strings.Contains(d.raw, `"entries":[`) {
		t.Fatalf("discovered %s", d.raw)
	}
	l := f.next(t, "launched")
	if l.Browser != id || l.Role != "seven" || l.PID <= 0 || !strings.HasPrefix(l.CDP, "http://127.0.0.1:") || filepath.Base(l.ProfileDir) != profile {
		t.Fatalf("launched %s", l.raw)
	}
	t.Cleanup(func() { _ = os.RemoveAll(l.ProfileDir) })
	a := f.next(t, "attached")
	if a.Browser != id || a.Role != "seven" || !a.Launched || a.CDP != l.CDP {
		t.Fatalf("attached %s", a.raw)
	}
	up := f.next(t, "linked")
	if up.Tab != pageURL || up.Peer != "link test page" || !strings.Contains(strings.Join(up.Caps, ","), "net") || up.Policy == "" {
		t.Fatalf("linked %s", up.raw)
	}

	p := dialPage(t, l.CDP, pageURL)
	echoOverLink(t, p)
	var hello struct {
		Name     string `json:"name"`
		Browsers []struct {
			ID    string `json:"id"`
			Seven bool   `json:"seven"`
		} `json:"browsers"`
	}
	p.eval("linkTest.hello()", &hello)
	if hello.Name != "it-default" || len(hello.Browsers) != 1 || !hello.Browsers[0].Seven {
		t.Fatalf("node HELLO %+v", hello)
	}

	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	lost := f.next(t, "link-lost")
	if lost.Reason != "slicc is exiting" {
		t.Fatalf("link-lost %s", lost.raw)
	}
	c := f.next(t, "closed")
	if len(c.Browsers) != 1 || c.Browsers[0] != id {
		t.Fatalf("closed %s", c.raw)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := getVersion(l.CDP); err == nil {
		t.Fatalf("the launched browser still answers at %s", l.CDP)
	}
	if strings.Contains(f.stderr.String(), "\x1b[") {
		t.Fatalf("ANSI codes on a non-terminal stderr: %q", f.stderr.String())
	}
}

func TestDefaultPlainOpensSevenInARunningBrowser(t *testing.T) {
	b := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	f := startFlow(t, append(env, "NO_COLOR=1"), "--plain", "--cdp", b.endpoint, "--browser", id, "--page", pageURL)
	f.waitStderr(t, "attached to")
	f.waitStderr(t, "opened "+pageURL+" in a new tab")
	f.waitStderr(t, `linked with "link test page" in `+pageURL)
	if n := countTabs(t, b.client, pageURL); n != 1 {
		t.Fatalf("%d tabs at %s", n, pageURL)
	}
	p := dialPage(t, b.endpoint, pageURL)
	echoOverLink(t, p)
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := f.stderr.String()
	if strings.Contains(out, "left seven") {
		t.Fatalf("the new tab's blank context was reported: %q", out)
	}
	if !strings.Contains(out, "slicc: done") || strings.Contains(out, "\x1b[") {
		t.Fatalf("stderr %q", out)
	}
	if _, err := getVersion(b.endpoint); err != nil {
		t.Fatalf("slicc closed a browser it only attached to: %v", err)
	}
	p.waitFor("!linkTest.status().up", 15*time.Second)
}

func TestDefaultAttachesToAnOpenSevenWithAnExtraBrowser(t *testing.T) {
	b := launchBrowser(t)
	extra := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	p := b.openPage(t, pageURL)
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	extraID := "cdp:127.0.0.1:" + strings.TrimPrefix(extra.endpoint, "http://127.0.0.1:")
	f := startFlow(t, env, "--json", "--cdp", b.endpoint, "--cdp", extra.endpoint, "--browser", id, "--browser", extraID, "--page", pageURL)
	d := f.next(t, "discovered")
	found := false
	for _, e := range d.Entries {
		if e.ID == id && e.Endpoint != nil && e.Endpoint.SevenOpen {
			found = true
		}
	}
	if !found {
		t.Fatalf("discovered %s has no %s with seven open", d.raw, id)
	}
	if a := f.next(t, "attached"); a.Browser != id || a.Role != "seven" || a.Launched {
		t.Fatalf("attached %s", a.raw)
	}
	if a := f.next(t, "attached"); a.Browser != extraID || a.Role != "extra" {
		t.Fatalf("attached %s", a.raw)
	}
	f.next(t, "linked")
	if n := countTabs(t, b.client, pageURL); n != 1 {
		t.Fatalf("%d tabs at %s; slicc should use the open one", n, pageURL)
	}
	echoOverLink(t, p)
	var hello struct {
		Browsers []struct {
			ID    string `json:"id"`
			Seven bool   `json:"seven"`
		} `json:"browsers"`
	}
	p.eval("linkTest.hello()", &hello)
	if len(hello.Browsers) != 2 || !hello.Browsers[0].Seven || hello.Browsers[1].Seven {
		t.Fatalf("HELLO browsers %+v", hello.Browsers)
	}
	_ = extra.client.Close()
	_ = extra.cmd.Process.Kill()
	if g := f.next(t, "browser-gone"); g.Browser != extraID || g.Role != "extra" {
		t.Fatalf("browser-gone %s", g.raw)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", extra.port), time.Second)
		if err != nil {
			break
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatalf("port %d still answers after the kill", extra.port)
		}
	}
	launchBrowserOn(t, extra.port)
	if g := f.next(t, "browser-back"); g.Browser != extraID || g.Role != "extra" {
		t.Fatalf("browser-back %s", g.raw)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	f.next(t, "link-lost")
	if c := f.next(t, "closed"); len(c.Browsers) != 0 || !strings.Contains(c.raw, `"browsers":[]`) {
		t.Fatalf("closed %s", c.raw)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestDefaultRejectsBadInput(t *testing.T) {
	env, _ := launchEnv(t)
	for _, args := range [][]string{
		{"--frobnicate"},
		{"--plain", "extra-arg"},
		{"--page", "ftp://example.com/"},
		{"--profile", "../x"},
		{"--kernel-port", "0"},
		{"--kernel-port", "x"},
	} {
		_, errOut, code := runWithEnv(t, env, args...)
		if code != 2 || errOut == "" {
			t.Errorf("%v: code %d, stderr %q", args, code, errOut)
		}
	}
	out, errOut, code := runWithEnv(t, env, "--json", "--browser", "no-such-browser", "--cdp", closedAddr(t))
	if code != 1 || !strings.Contains(out, `"event":"error"`) || !strings.Contains(out, `"schema":1`) {
		t.Fatalf("unknown browser: code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestDefaultServesKernelServices(t *testing.T) {
	b := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/?kernel"
	port := freePort(t)
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	f := startFlow(t, env, "--plain", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--kernel-port", strconv.Itoa(port))
	f.waitStderr(t, fmt.Sprintf("kernel services on http://<port>.kernel.localhost:%d/", port))
	k := &kernelEnv{port: port, addr: fmt.Sprintf("127.0.0.1:%d", port)}
	r := decodeReply(t, k.get(t, k.host(5173), "/hello"))
	if r.Port != 5173 || r.Path != "/hello" {
		t.Fatalf("kernel reply %+v", r)
	}
	p := dialPage(t, b.endpoint, pageURL)
	var h struct {
		Policy struct {
			Deny []string `json:"deny"`
		} `json:"policy"`
	}
	p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Policy.Deny, fmt.Sprintf("127.0.0.1/32:%d", port)) {
		t.Fatalf("policy deny %v lacks the kernel port", h.Policy.Deny)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	f.wait(t)
	if conn, err := net.DialTimeout("tcp", k.addr, time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("the kernel port %d still listens", port)
	}
}

func brokenCDP(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = io.WriteString(w, `{"Browser":"Chrome/150.0.0.0","Protocol-Version":"1.3","User-Agent":"Mozilla/5.0 Chrome/150.0.0.0","webSocketDebuggerUrl":"ws://`+closedAddr(t)+`/devtools/browser/x"}`)
		case "/json/list":
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDefaultTelemetry(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := isolatedEnv(t, append(col.env(), "SLICC_NO_UPDATE_CHECK=1")...)
	r := runBin(t, env, bin, "--json", "--browser", "no-such-browser")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	enter := col.find("enter")
	if enter == nil || enter.Source != "default" {
		t.Fatalf("beacons: %+v", col.got())
	}
	if col.find("error") != nil {
		t.Fatalf("an unknown --browser is a usage error, not an operational one: %+v", col.got())
	}

	col = newCollector(t)
	env = isolatedEnv(t, append(col.env(), "SLICC_NO_UPDATE_CHECK=1")...)
	ep := brokenCDP(t)
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(ep, "http://127.0.0.1:")
	r = runBin(t, env, bin, "--json", "--cdp", ep, "--browser", id)
	if r.code != 1 || !strings.Contains(r.stdout, `"event":"error"`) {
		t.Fatalf("%+v", r)
	}
	failure := col.find("error")
	if failure == nil || failure.Source != "browser" {
		t.Fatalf("beacons: %+v", col.got())
	}
	if strings.Contains(failure.raw, ep) {
		t.Fatalf("the beacon carries the endpoint URL: %s", failure.raw)
	}
}

func TestDefaultExitsWhenTheSevenBrowserGoesAway(t *testing.T) {
	b := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	f := startFlow(t, env, "--json", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--no-kernel")
	f.next(t, "discovered")
	f.next(t, "attached")
	f.next(t, "linked")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.client.Call(ctx, "", "Browser.close", nil, nil)
	f.next(t, "link-lost")
	if c := f.next(t, "closed"); len(c.Browsers) != 0 {
		t.Fatalf("closed %s", c.raw)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestDefaultOffersComputeWithARunner(t *testing.T) {
	b := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	args := append([]string{"--plain", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--no-kernel"}, runnerArgs()...)
	f := startFlow(t, env, args...)
	f.waitStderr(t, "compute: ssh via "+strings.Join(helperRunner(), " "))
	f.waitStderr(t, "linked with")
	p := dialPage(t, b.endpoint, pageURL)
	var h struct {
		Caps       []string `json:"caps"`
		SSHHostKey string   `json:"sshHostKey"`
	}
	p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Caps, "ssh") || !strings.HasPrefix(h.SSHHostKey, "ssh-ed25519 ") {
		t.Fatalf("HELLO caps %v, host key %q", h.Caps, h.SSHHostKey)
	}
	for _, bad := range [][]string{{"--shell", "sh"}, {"--runner"}, {"--sftp"}} {
		_, errOut, code := runWithEnv(t, env, bad...)
		if code != 2 || !strings.Contains(errOut, "runner") {
			t.Errorf("%v: code %d, stderr %q", bad, code, errOut)
		}
	}
}
