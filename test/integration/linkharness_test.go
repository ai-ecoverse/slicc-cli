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

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const harnessChromeNotice = "the harness never launches this machine's Chrome off CI: set SLICC_TEST_CHROME to a Chrome for Testing executable to run this test"

func findChrome() string {
	if p := os.Getenv("SLICC_TEST_CHROME"); p != "" {
		return p
	}
	if os.Getenv("CI") == "" {
		return ""
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
	life     *lifeline
	stderr   string
}

func launchBrowser(t *testing.T) *chromeProc {
	t.Helper()
	return launchBrowserOn(t, 0)
}

func launchBrowserOn(t *testing.T, debugPort int, extra ...string) *chromeProc {
	t.Helper()
	return launchBrowserWith(t, debugPort, nil, extra...)
}

func launchBrowserWith(t *testing.T, debugPort int, prep func(cmd *exec.Cmd) func(), extra ...string) *chromeProc {
	t.Helper()
	chrome := findChrome()
	if chrome == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("no Chrome found; set SLICC_TEST_CHROME")
		}
		t.Skip(harnessChromeNotice)
	}
	dir, err := os.MkdirTemp("", "slicc-it-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--headless=new", "--remote-debugging-port=" + strconv.Itoa(debugPort), "--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync",
	}
	args = append(args, browser.ThrowawaySwitches...)
	args = append(append(args, extra...), "about:blank")
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(chrome, args...)
	cmd.Env = realEnv
	stderr := filepath.Join(dir, "slicc-it-chrome.log")
	logFile, err := os.Create(stderr)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	tie := tieToTest(cmd)
	var afterStart func()
	if prep != nil {
		afterStart = prep(cmd)
	}
	err = cmd.Start()
	_ = logFile.Close()
	if afterStart != nil {
		afterStart()
	}
	if err != nil {
		t.Fatal(err)
	}
	life, err := tie()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("tie Chrome's life to the test binary: %v", err)
	}
	t.Cleanup(func() {
		life.cut()
		reap(t, cmd, "Chrome")
		_ = os.RemoveAll(dir)
	})
	port := 0
	started := time.Now()
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
		t.Fatalf("Chrome did not write DevToolsActivePort in %s; %s\n%s%s", time.Since(started).Round(time.Millisecond), userDataFiles(dir), chromeOutput(stderr), chromeTree(cmd.Process.Pid))
	}
	b := &chromeProc{endpoint: fmt.Sprintf("http://127.0.0.1:%d", port), port: port, cmd: cmd, life: life, stderr: stderr}
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
	p, err := b.tryOpenPage(t, url)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func launchWithPage(t *testing.T, url string) (*chromeProc, *page) {
	t.Helper()
	b := launchBrowser(t)
	p, err := b.tryOpenPage(t, url)
	if err == nil {
		return b, p
	}
	if b.answers() {
		t.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "%s: the fresh Chrome stopped answering CDP before anything but this harness touched it; relaunching it once:\n%v\n", t.Name(), err)
	b.life.cut()
	reap(t, b.cmd, "Chrome")
	b = launchBrowser(t)
	return b, b.openPage(t, url)
}

func (b *chromeProc) answers() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.client.Call(ctx, "", "Browser.getVersion", nil, nil) == nil
}

func (b *chromeProc) tryOpenPage(t *testing.T, url string) (*page, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &res); err != nil {
		return nil, fmt.Errorf("Target.createTarget %s: %w", url, err)
	}
	s, err := b.client.Attach(ctx, res.TargetID)
	if err != nil {
		return nil, fmt.Errorf("attach to %s: %w", url, err)
	}
	t.Cleanup(func() { b.closeTarget(res.TargetID) })
	p := &page{t: t, b: b, target: res.TargetID, session: s}
	err = p.loaded(url, 15*time.Second)
	if err == nil || !p.droppedModule() {
		return p, err
	}
	fmt.Fprintf(os.Stderr, "%s: Chrome failed a module fetch of %s that never reached the page server; reloading the page once:\n%v\n", t.Name(), url, err)
	rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer rcancel()
	if rerr := s.Call(rctx, "Page.reload", map[string]any{"ignoreCache": true}, nil); rerr != nil {
		return p, fmt.Errorf("%w; Page.reload: %v", err, rerr)
	}
	return p, p.loaded(url, 15*time.Second)
}

func (p *page) droppedModule() bool {
	var dropped bool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.session.Evaluate(ctx, `typeof globalThis.linkTest !== 'object' && document.readyState === 'complete' && performance.getEntriesByType('resource').some((e) => e.name.endsWith('.js') && e.responseStatus === 0)`, cdp.EvaluateOptions{ReturnByValue: true}, &dropped)
	return err == nil && dropped
}

func (p *page) waitLoaded(url string, timeout time.Duration) {
	p.t.Helper()
	if err := p.loaded(url, timeout); err != nil {
		p.t.Fatal(err)
	}
}

func (p *page) loaded(url string, timeout time.Duration) error {
	start := time.Now()
	err := p.until("typeof globalThis.linkTest === 'object'", timeout)
	if err == nil {
		return nil
	}
	var state any
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serr := p.session.Evaluate(ctx, `({ href: location.href, readyState: document.readyState, resources: performance.getEntriesByType('resource').map((e) => e.name + ' ' + e.responseStatus + ' ' + Math.round(e.duration) + 'ms') })`, cdp.EvaluateOptions{ReturnByValue: true}, &state)
	return fmt.Errorf("%s did not load in %s (%v); page state %v (%v); page server since %s:\n%s%s", url, timeout, err, state, serr, start.Format("15:04:05.000"), pageRequests.since(start.Add(-time.Second)), p.b.report())
}

const closeTargetTimeout = 5 * time.Second

func (b *chromeProc) closeTarget(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTargetTimeout)
	defer cancel()
	_ = b.client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": id}, nil)
}

func (b *chromeProc) report() string {
	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	var version struct {
		Product string `json:"product"`
	}
	stuck := b.client.Call(ctx, "", "Browser.getVersion", nil, &version)
	fmt.Fprintf(&out, "Browser.getVersion over CDP: %q (%v) after %s\n", version.Product, stuck, time.Since(start).Round(time.Millisecond))
	var targets struct {
		TargetInfos []struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			Attached bool   `json:"attached"`
		} `json:"targetInfos"`
	}
	tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer tcancel()
	err := b.client.Call(tctx, "", "Target.getTargets", nil, &targets)
	fmt.Fprintf(&out, "Target.getTargets: %+v (%v)\n", targets.TargetInfos, err)
	out.WriteString(chromeOutput(b.stderr))
	out.WriteString(chromeTree(b.cmd.Process.Pid))
	if stuck != nil && runtime.GOOS == "darwin" {
		out.WriteString(chromeSample(b.cmd.Process.Pid))
	}
	return out.String()
}

func chromeOutput(name string) string {
	log, err := os.ReadFile(name)
	lines := strings.Split(strings.TrimRight(string(log), "\n"), "\n")
	if len(lines) > 60 {
		lines = lines[len(lines)-60:]
	}
	return fmt.Sprintf("Chrome output (%v):\n  %s\n", err, strings.Join(lines, "\n  "))
}

func userDataFiles(dir string) string {
	entries, err := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return fmt.Sprintf("the user-data dir holds [%s] (%v)", strings.Join(names, " "), err)
}

func (p *page) eval(expr string, out any) {
	p.t.Helper()
	p.evalWithin(expr, out, 90*time.Second)
}

func (p *page) evalWithin(expr string, out any, timeout time.Duration) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

func (p *page) until(expr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := p.session.Evaluate(ctx, "Boolean("+expr+")", cdp.EvaluateOptions{ReturnByValue: true}, &ok)
		cancel()
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

func startAttach(t *testing.T, args ...string) *attachProc {
	t.Helper()
	return startAttachEnv(t, false, nil, args...)
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
