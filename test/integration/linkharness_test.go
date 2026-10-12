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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func findChrome() string {
	if p := os.Getenv("SLICC_TEST_CHROME"); p != "" {
		return p
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if dir := realGetenv(env); dir != "" {
				candidates = append(candidates, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	default:
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

type chromeProc struct {
	endpoint string
	port     int
	client   *cdp.Client
	cmd      *exec.Cmd
}

func launchBrowser(t *testing.T) *chromeProc {
	t.Helper()
	return launchBrowserOn(t, 0)
}

func launchBrowserOn(t *testing.T, debugPort int) *chromeProc {
	t.Helper()
	chrome := findChrome()
	if chrome == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("no Chrome found; set SLICC_TEST_CHROME")
		}
		t.Skip("no Chrome found; set SLICC_TEST_CHROME")
	}
	dir, err := os.MkdirTemp("", "slicc-it-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--headless=new", "--remote-debugging-port=" + strconv.Itoa(debugPort), "--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "about:blank",
	}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(chrome, args...)
	cmd.Env = realEnv
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reap(t, cmd, "Chrome")
		_ = os.RemoveAll(dir)
	})
	port := 0
	for deadline := time.Now().Add(30 * time.Second); debugPort != 0 && time.Now().Before(deadline) && port == 0; time.Sleep(100 * time.Millisecond) {
		if _, err := getVersion(fmt.Sprintf("http://127.0.0.1:%d", debugPort)); err == nil {
			port = debugPort
		}
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && port == 0; time.Sleep(100 * time.Millisecond) {
		b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if err == nil {
			port, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
		}
	}
	if port == 0 {
		t.Fatal("Chrome did not write DevToolsActivePort")
	}
	b := &chromeProc{endpoint: fmt.Sprintf("http://127.0.0.1:%d", port), port: port, cmd: cmd}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b.client, err = cdp.Dial(ctx, b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.client.Close() })
	return b
}

type page struct {
	t       *testing.T
	b       *chromeProc
	target  string
	session *cdp.Session
	rehello func()
}

func (b *chromeProc) openPage(t *testing.T, url string) *page {
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
	p := &page{t: t, b: b, target: res.TargetID, session: s}
	p.waitLoaded(url, 15*time.Second)
	return p
}

func (p *page) waitLoaded(url string, timeout time.Duration) {
	p.t.Helper()
	start := time.Now()
	err := p.until("typeof globalThis.linkTest === 'object'", timeout)
	if err == nil {
		if d := time.Since(start); d > 3*time.Second {
			p.t.Logf("SLOWLOAD %s loaded in %s; page server:\n%s\npolls:\n%s", url, d, pageRequests.since(start.Add(-time.Second)), pollTrace.since(start))
		}
		return
	}
	var state any
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serr := p.session.Evaluate(ctx, `({ href: location.href, readyState: document.readyState, resources: performance.getEntriesByType('resource').map((e) => e.name + ' ' + Math.round(e.duration) + 'ms') })`, cdp.EvaluateOptions{ReturnByValue: true}, &state)
	p.t.Fatalf("%s did not load in %s (%v); page state %v (%v); page server since %s:\n%s\npolls:\n%s", url, timeout, err, state, serr, start.Format("15:04:05.000"), pageRequests.since(start.Add(-time.Second)), pollTrace.since(start))
}

func (p *page) eval(expr string, out any) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := p.session.Evaluate(ctx, expr, cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, out); err != nil {
		p.t.Fatalf("evaluate %s: %v", expr, err)
	}
}

func (p *page) waitFor(expr string, timeout time.Duration) {
	p.t.Helper()
	if err := p.until(expr, timeout); err != nil {
		p.t.Fatal(err)
	}
}

var pollTrace = &requestLog{}

func (p *page) until(expr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t0 := time.Now()
		err := p.session.Evaluate(ctx, "Boolean("+expr+")", cdp.EvaluateOptions{ReturnByValue: true}, &ok)
		cancel()
		pollTrace.add(fmt.Sprintf("eval %s ok=%v err=%v", time.Since(t0).Round(time.Millisecond), ok, err))
		if err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s (last error %v)", expr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *page) reload() {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.session.Call(ctx, "Page.reload", map[string]any{}, nil); err != nil {
		p.t.Fatal(err)
	}
	p.waitFor("typeof globalThis.linkTest === 'object' && document.readyState === 'complete'", 15*time.Second)
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type attachProc struct {
	cmd    *exec.Cmd
	stderr *syncBuffer
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startAttach(t *testing.T, stdio bool, args ...string) *attachProc {
	t.Helper()
	return startAttachEnv(t, stdio, nil, args...)
}

func startAttachEnv(t *testing.T, stdio bool, env []string, args ...string) *attachProc {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"attach"}, args...)...)
	cmd.Env = append(append(os.Environ(), "SLICC_DEBUG=1"), env...)
	a := &attachProc{cmd: cmd, stderr: &syncBuffer{}}
	cmd.Stderr = a.stderr
	if stdio {
		var err error
		if a.stdin, err = cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
		if a.stdout, err = cmd.StdoutPipe(); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reap(t, cmd, "slicc attach")
		if t.Failed() {
			t.Logf("slicc attach stderr:\n%s", a.stderr.String())
		}
	})
	return a
}

func (a *attachProc) waitStderr(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(a.stderr.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("stderr never said %q:\n%s", want, a.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func relayStdio(t *testing.T, p *page, a *attachProc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sub := p.b.client.Subscribe()
	t.Cleanup(sub.Close)
	if err := p.session.AddBinding(ctx, "sliccLinkSend"); err != nil {
		t.Fatal(err)
	}
	if err := p.session.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	var stdinMu sync.Mutex
	go func() {
		for ev := range sub.Events() {
			if ev.SessionID != p.session.ID || ev.Method != "Runtime.bindingCalled" {
				continue
			}
			var bc cdp.BindingCalled
			if json.Unmarshal(ev.Params, &bc) != nil || bc.Name != "sliccLinkSend" {
				continue
			}
			stdinMu.Lock()
			_, _ = io.WriteString(a.stdin, bc.Payload+"\n")
			stdinMu.Unlock()
		}
	}()
	go func() {
		sc := bufio.NewScanner(a.stdout)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			expr := "typeof globalThis.sliccLinkReceive === 'function' ? (globalThis.sliccLinkReceive(" + line + "), true) : false"
			for i := 0; i < 300; i++ {
				var ok bool
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := p.session.Evaluate(c, expr, cdp.EvaluateOptions{ReturnByValue: true}, &ok)
				cancel()
				if err == nil && ok {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	p.rehello = func() {
		stdinMu.Lock()
		defer stdinMu.Unlock()
		_, _ = io.WriteString(a.stdin, `{"t":"hello"}`+"\n")
	}
}

func serveTCP(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				serve(c)
			}()
		}
	}()
	return ln.Addr().String()
}

func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

type requestLog struct {
	mu    sync.Mutex
	lines []timedLine
}

type timedLine struct {
	at   time.Time
	line string
}

func (l *requestLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, timedLine{time.Now(), line})
}

func (l *requestLog) since(t time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, e := range l.lines {
		if !e.at.Before(t) {
			fmt.Fprintf(&b, "  %s %s\n", e.at.Format("15:04:05.000"), e.line)
		}
	}
	return b.String()
}

var pageRequests = &requestLog{}

func pageServer(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "link")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageRequests.add(r.Host + " " + r.Method + " " + r.URL.RequestURI())
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

func jsArgs(args ...any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a)
		parts[i] = string(b)
	}
	return strings.Join(parts, ", ")
}
