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
			if dir := os.Getenv(env); dir != "" {
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
}

func launchBrowser(t *testing.T) *chromeProc {
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
		"--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "about:blank",
	}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(chrome, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
	})
	port := 0
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && port == 0; time.Sleep(100 * time.Millisecond) {
		b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if err == nil {
			port, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
		}
	}
	if port == 0 {
		t.Fatal("Chrome did not write DevToolsActivePort")
	}
	b := &chromeProc{endpoint: fmt.Sprintf("http://127.0.0.1:%d", port), port: port}
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
	p.waitFor("typeof globalThis.linkTest === 'object'", 15*time.Second)
	return p
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
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := p.session.Evaluate(ctx, "Boolean("+expr+")", cdp.EvaluateOptions{ReturnByValue: true}, &ok)
		cancel()
		if err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("timed out waiting for %s (last error %v)", expr, err)
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
	cmd := exec.Command(binary, append([]string{"attach"}, args...)...)
	cmd.Env = append(os.Environ(), "SLICC_DEBUG=1")
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
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
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

func pageServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join("testdata", "link"))))
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
