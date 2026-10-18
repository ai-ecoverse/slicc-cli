package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const fakeDebugBody = "fake debugging port"

type debugPorts struct {
	set      map[string]string
	config   string
	ranged   int
	exposed  int
	found    int
	foundExp int
	late     int
	proc     int
	given    int
}

func serveFakeDebug(t *testing.T, ln net.Listener) int {
	t.Helper()
	port := ln.Addr().(*net.TCPAddr).Port
	var conns, requests atomic.Int32
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			_, _ = io.WriteString(w, fakeDebugBody)
		}),
		ConnState: func(_ net.Conn, st http.ConnState) {
			if st == http.StateNew {
				conns.Add(1)
			}
		},
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if t.Failed() {
			t.Logf("fake debugging port %d: %d connections, %d requests", port, conns.Load(), requests.Load())
		}
	})
	return port
}

func fakeDebugPair(t *testing.T) (int, int) {
	t.Helper()
	for try := 0; try < 50; try++ {
		a, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := a.Addr().(*net.TCPAddr).Port
		b, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port+1))
		if err != nil {
			_ = a.Close()
			continue
		}
		return serveFakeDebug(t, a), serveFakeDebug(t, b)
	}
	t.Fatal("no two free ports in a row")
	return 0, 0
}

func fakeDebug(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return serveFakeDebug(t, ln)
}

func newDebugPorts(t *testing.T) *debugPorts {
	t.Helper()
	root := t.TempDir()
	set := map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"APPDATA":         filepath.Join(root, "Roaming"),
		"LOCALAPPDATA":    filepath.Join(root, "Local"),
		"SLICC_DEBUG":     "1",
	}
	config := set["XDG_CONFIG_HOME"]
	switch runtime.GOOS {
	case "darwin":
		config = filepath.Join(root, "Library", "Application Support")
	case "windows":
		config = set["APPDATA"]
	}
	d := &debugPorts{set: set, config: filepath.Join(config, "slicc", "seven-profiles")}
	d.ranged, d.exposed = fakeDebugPair(t)
	d.found, d.foundExp, d.late, d.proc = fakeDebug(t), fakeDebug(t), fakeDebug(t), fakeDebug(t)
	d.given = fakeCDPVersion(t, closedPort(t)).Listener.Addr().(*net.TCPAddr).Port
	d.writeActivePort(t, "found", d.found)
	d.writeActivePort(t, "exposed", d.foundExp)
	set[browser.DiscoveryEnv] = "home"
	set[browser.ProtectedPortsEnv] = fmt.Sprintf("%d-%d", d.ranged, d.exposed)
	return d
}

func (d *debugPorts) writeActivePort(t *testing.T, name string, port int) {
	t.Helper()
	dir := filepath.Join(d.config, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/%s\n", port, name)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (d *debugPorts) givenURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(d.given)
}

func (d *debugPorts) env(base []string) []string {
	return overrideEnv(base, d.set)
}

func (d *debugPorts) list() []string {
	var out []string
	for k, v := range d.set {
		out = append(out, k+"="+v)
	}
	return out
}

func (d *debugPorts) allowArgs() []string {
	var args []string
	for _, port := range []int{d.ranged, d.found, d.late, d.proc, d.given} {
		args = append(args, "--allow", "127.0.0.1:"+strconv.Itoa(port))
	}
	return append(args, "--allow", "127.0.0.0/8")
}

func (d *debugPorts) exposeArgs() []string {
	return []string{"--expose-debug-port", strconv.Itoa(d.exposed), "--expose-debug-port", strconv.Itoa(d.foundExp)}
}

func (d *debugPorts) check(t *testing.T, p *page, alsoRefused ...int) {
	t.Helper()
	var hello struct {
		Policy struct {
			Deny    []string `json:"deny"`
			Summary string   `json:"summary"`
		} `json:"policy"`
	}
	p.eval("linkTest.ready()", &hello)
	for _, port := range []int{d.ranged, d.found} {
		if want := fmt.Sprintf("127.0.0.1/32:%d", port); !slices.Contains(hello.Policy.Deny, want) {
			t.Fatalf("policy deny %v lacks %s", hello.Policy.Deny, want)
		}
	}
	for _, port := range []int{d.exposed, d.foundExp} {
		if bad := fmt.Sprintf("127.0.0.1/32:%d", port); slices.Contains(hello.Policy.Deny, bad) {
			t.Fatalf("policy deny %v has the exposed %s", hello.Policy.Deny, bad)
		}
	}
	want := fmt.Sprintf("exposes debugging ports %d, %d", d.exposed, d.foundExp)
	if !strings.Contains(hello.Policy.Summary, want) {
		t.Fatalf("policy summary %q lacks %q", hello.Policy.Summary, want)
	}
	refusedOverLink(t, p, append([]int{d.ranged, d.found}, alsoRefused...)...)
	for _, port := range []int{d.exposed, d.foundExp} {
		var r echoResult
		p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": "127.0.0.1", "port": port})+")", &r)
		if r.Reset != "" || r.Error != "" || r.Opened.RemoteAddr == "" {
			t.Fatalf("net to the exposed debugging port %d: %+v", port, r)
		}
		var hr httpResult
		url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
		p.eval("linkTest.http("+jsArgs(map[string]any{"url": url, "method": "GET", "headers": [][2]string{}})+")", &hr)
		if hr.Status != 200 || hr.body(t) != fakeDebugBody {
			t.Fatalf("http to the exposed debugging port %s: %+v", url, hr)
		}
	}
}

func refusedOverLink(t *testing.T, p *page, ports ...int) {
	t.Helper()
	hosts := []string{"127.0.0.1"}
	if lan, ok := findLANAddress(); ok {
		hosts = append(hosts, lan.String())
	}
	for _, port := range ports {
		for _, host := range hosts {
			var r echoResult
			p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": host, "port": port})+")", &r)
			if r.Reset != "EACCES" {
				t.Fatalf("net to the debugging port %s:%d: %+v", host, port, r)
			}
		}
		var hr httpResult
		url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
		p.eval("linkTest.http("+jsArgs(map[string]any{"url": url, "method": "GET", "headers": [][2]string{}})+")", &hr)
		if hr.Reset != "EACCES" {
			t.Fatalf("http to the debugging port %s: %+v", url, hr)
		}
	}
}

func (d *debugPorts) lateBrowsers(t *testing.T, p *page) {
	t.Helper()
	d.writeActivePort(t, "late", d.late)
	refusedOverLink(t, p, d.late)
	exe := buildFakeChrome(t).Executable
	start := func(port int) string {
		dir := t.TempDir()
		cmd := exec.Command(exe, "--remote-debugging-port="+strconv.Itoa(port), "--user-data-dir="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return dir
	}
	start(d.proc)
	dir := start(0)
	var own int
	for deadline := time.Now().Add(20 * time.Second); own == 0; time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			own, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake browser never wrote DevToolsActivePort")
		}
	}
	time.Sleep(1100 * time.Millisecond)
	refusedOverLink(t, p, d.proc, own)
}

func TestAttachDeniesTheHostsDebuggingPorts(t *testing.T) {
	d := newDebugPorts(t)
	args := append(append([]string{"--cdp-extra", d.givenURL()}, d.allowArgs()...), d.exposeArgs()...)
	env := setupLinkEnv(t, false, d.list(), args...)
	d.check(t, env.p, env.b.port, d.given)
	d.lateBrowsers(t, env.p)
}

func TestDefaultDeniesTheHostsDebuggingPorts(t *testing.T) {
	d := newDebugPorts(t)
	b := launchBrowser(t)
	base, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	id := "cdp:127.0.0.1:" + strconv.Itoa(b.port)
	args := append([]string{"--plain", "--no-kernel", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--allow", "127.0.0.1:" + strconv.Itoa(b.port)}, d.allowArgs()...)
	f := startFlow(t, d.env(base), append(args, d.exposeArgs()...)...)
	f.waitStderr(t, `linked with "link test page" in `+pageURL)
	p := dialPage(t, b.endpoint, pageURL)
	d.check(t, p, b.port)
	d.lateBrowsers(t, p)
}

func TestOpenDeniesTheHostsDebuggingPorts(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	e := newLoopEnv(t)
	d := newDebugPorts(t)
	e.env = d.env(e.env)
	pageURL := srv.URL + "/index.html?ports=" + e.ports()
	o := startLoop(t, e, "pair", append(append([]string{"--page", pageURL, "--no-kernel"}, d.allowArgs()...), d.exposeArgs()...)...)
	p := b.openPage(t, o.url(t))
	if r := confirm(t, p, ""); !r.Linked {
		t.Fatalf("pairing: %+v", r)
	}
	d.check(t, p)
	d.lateBrowsers(t, p)
}

func TestFollowDeniesTheHostsDebuggingPorts(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	d := newDebugPorts(t)
	args := append(append([]string{"--exit", "--cdp", d.givenURL(), "--allow", "127.0.0.1/32"}, d.allowArgs()...), d.exposeArgs()...)
	if lan, ok := findLANAddress(); ok {
		args = append(args, "--allow", lan.String())
	}
	fp := startFollow(t, d.env(env), h.joinURL(trays[0]), args...)
	fp.waitStderr(t, "exit on", 15*time.Second)
	p.waitUp(t, "")
	d.check(t, p, d.given)
	d.lateBrowsers(t, p)
}

func TestExposeDebugPortRejectsABadPort(t *testing.T) {
	for _, args := range [][]string{
		{"attach", "--signal", "stdio", "--expose-debug-port", "0"},
		{"open", "--expose-debug-port", "nope"},
		{"--plain", "--expose-debug-port", "70000"},
	} {
		_, errOut, code := runWithEnv(t, os.Environ(), args...)
		if code != 2 || !strings.Contains(errOut, "expose-debug-port") {
			t.Fatalf("%v: code %d, stderr %q", args, code, errOut)
		}
	}
	d := newDebugPorts(t)
	plain := fakeDebug(t)
	for args, want := range map[string]string{
		strconv.Itoa(plain):   fmt.Sprintf("--expose-debug-port %d: not a debugging port", plain),
		strconv.Itoa(d.given): fmt.Sprintf("--expose-debug-port %d: slicc drives that browser", d.given),
	} {
		_, errOut, code := runWithEnv(t, d.env(os.Environ()), "attach", "--signal", "stdio", "--no-kernel", "--no-jerk", "--cdp-extra", d.givenURL(), "--expose-debug-port", args)
		if code != 1 || !strings.Contains(errOut, want) {
			t.Fatalf("--expose-debug-port %s: code %d, stderr %q", args, code, errOut)
		}
	}
}

func TestEveryModeDocumentsTheDebuggingPorts(t *testing.T) {
	for _, args := range [][]string{{"attach", "--help"}, {"open", "--help"}, {"pair", "--help"}, {"--help"}, {"https://hub.invalid/join/fake-token-0001", "follow", "--help"}} {
		out, errOut, _ := runWithEnv(t, os.Environ(), args...)
		help := out + errOut
		for _, want := range []string{"--expose-debug-port <port>", "9222-9229", "not even --allow 127.0.0.1:9222"} {
			if !strings.Contains(help, want) {
				t.Fatalf("%v help lacks %q:\n%s", args, want, help)
			}
		}
	}
}

func TestABadProtectedPortRangeExits(t *testing.T) {
	bad := overrideEnv(os.Environ(), map[string]string{browser.ProtectedPortsEnv: "9310-9300"})
	_, errOut, code := runWithEnv(t, bad, "attach", "--signal", "stdio", "--no-kernel", "--no-jerk")
	if code != 1 || !strings.Contains(errOut, browser.ProtectedPortsEnv) {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func debugProcessTable(lines func(context.Context) string) func(context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		s := lines(ctx)
		switch s {
		case "fail":
			return nil, os.ErrNotExist
		case "hang":
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if runtime.GOOS != "windows" {
			return []byte(s), nil
		}
		var rows []string
		for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
			pid, cmd, _ := strings.Cut(strings.TrimSpace(l), " ")
			rows = append(rows, fmt.Sprintf(`{"ProcessId":%s,"CommandLine":%q}`, pid, cmd))
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil
	}
}

type debugLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *debugLog) logf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, a...))
}

func (l *debugLog) count(want string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}

func (l *debugLog) waitFor(t *testing.T, want string, limit time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(limit); l.count(want) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			l.mu.Lock()
			defer l.mu.Unlock()
			t.Fatalf("no %q in %v", want, l.lines)
		}
	}
}

const (
	procListFailed    = "couldn't list running browsers for their debugging ports:"
	procListRecovered = "listing running browsers for their debugging ports again"
	procListRefused   = "a browser debugging port: couldn't list running browsers"
)

func startGuard(t *testing.T, table func(context.Context) ([]byte, error), logf func(string, ...any)) (*policy.Dialer, context.CancelFunc) {
	t.Helper()
	t.Setenv(browser.DiscoveryEnv, "isolated")
	t.Setenv(browser.ProtectedPortsEnv, "9300")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pol := policy.Local()
	errCh := make(chan error, 1)
	go func() {
		errCh <- browser.Guard(ctx, pol, browser.Options{Home: t.TempDir(), Processes: table}, nil, nil, logf)
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Guard never returned")
	}
	return policy.NewDialer(pol), cancel
}

func connectAsync(d *policy.Dialer, addr string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- d.Control("tcp4", addr, nil) }()
	return done
}

func connectWithin(t *testing.T, d *policy.Dialer, addr string, limit time.Duration) error {
	t.Helper()
	select {
	case err := <-connectAsync(d, addr):
		return err
	case <-time.After(limit):
		t.Fatalf("a connect to %s took longer than %v", addr, limit)
		return nil
	}
}

func refusedAsDebugPort(t *testing.T, err error, addr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "a browser debugging port") {
		t.Fatalf("a connect to %s: %v, want refused as a browser debugging port", addr, err)
	}
}

func TestProcessListScanWaitsWhenTheListIsStale(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	var calls atomic.Int32
	d, _ := startGuard(t, debugProcessTable(func(context.Context) string {
		if calls.Add(1) == 1 {
			return "21 /opt/chrome --remote-debugging-port=9370\n"
		}
		<-gate
		return "21 /opt/chrome --remote-debugging-port=9370\n22 /opt/chrome --remote-debugging-port=9380\n"
	}), t.Logf)
	time.Sleep(1500 * time.Millisecond)
	waiting := connectAsync(d, "127.0.0.1:9380")
	select {
	case err := <-waiting:
		t.Fatalf("a connect to this machine with a stale process list didn't wait for the scan: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := connectWithin(t, d, "8.8.8.8:9380", 200*time.Millisecond); err != nil {
		t.Fatalf("a connect to another host: %v", err)
	}
	release()
	select {
	case err := <-waiting:
		refusedAsDebugPort(t, err, "127.0.0.1:9380")
	case <-time.After(3 * time.Second):
		t.Fatal("the connect never finished after the scan did")
	}
	if err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second); err != nil {
		t.Fatalf("a connect to a port no browser listens on: %v", err)
	}
	refusedAsDebugPort(t, connectWithin(t, d, "127.0.0.1:9370", 3*time.Second), "127.0.0.1:9370")
}

func TestProcessListScanKeepsTheListFreshWhileActive(t *testing.T) {
	var calls atomic.Int32
	var slow atomic.Bool
	d, _ := startGuard(t, debugProcessTable(func(context.Context) string {
		calls.Add(1)
		if slow.Load() {
			time.Sleep(200 * time.Millisecond)
		}
		return "21 /opt/chrome --remote-debugging-port=9370\n"
	}), t.Logf)
	if err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	time.Sleep(3 * time.Second)
	if n := calls.Load() - before; n < 2 {
		t.Fatalf("%d process scans in the 3 s after a connect, want about one a second", n)
	}
	slow.Store(true)
	time.Sleep(1500 * time.Millisecond)
	waited := 0
	for range 20 {
		start := time.Now()
		if err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 150*time.Millisecond {
			waited++
		}
		time.Sleep(100 * time.Millisecond)
	}
	if waited > 5 {
		t.Fatalf("%d of 20 connects during steady use waited for a 200 ms process scan", waited)
	}
}

func TestProcessListScanFailureRefusesTheConnect(t *testing.T) {
	var mu sync.Mutex
	mode := "start"
	set := func(m string) { mu.Lock(); mode = m; mu.Unlock() }
	log := &debugLog{}
	d, _ := startGuard(t, debugProcessTable(func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		switch mode {
		case "fail":
			return "fail"
		case "late":
			return "21 /opt/chrome --remote-debugging-port=9370\n22 /opt/chrome --remote-debugging-port=9380\n"
		}
		return "21 /opt/chrome --remote-debugging-port=9370\n"
	}), log.logf)
	set("fail")
	time.Sleep(1500 * time.Millisecond)
	err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), procListRefused) {
		t.Fatalf("a connect to this machine while the process scan fails: %v", err)
	}
	if err := connectWithin(t, d, "8.8.8.8:9400", 200*time.Millisecond); err != nil {
		t.Fatalf("a connect to another host: %v", err)
	}
	log.waitFor(t, procListFailed, 3*time.Second)
	time.Sleep(2500 * time.Millisecond)
	if err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second); err == nil || !strings.Contains(err.Error(), procListRefused) {
		t.Fatalf("a connect to this machine after more failed scans: %v", err)
	}
	if n := log.count(procListFailed); n != 1 {
		t.Fatalf("the failure was reported %d times: %v", n, log.lines)
	}
	set("late")
	log.waitFor(t, procListRecovered, 5*time.Second)
	if err := connectWithin(t, d, "127.0.0.1:9400", 3*time.Second); err != nil {
		t.Fatalf("a connect after the scan recovered: %v", err)
	}
	refusedAsDebugPort(t, connectWithin(t, d, "127.0.0.1:9380", 3*time.Second), "127.0.0.1:9380")
	if n := log.count(procListRecovered); n != 1 {
		t.Fatalf("the recovery was reported %d times: %v", n, log.lines)
	}
}

func TestProcessListScanTimeoutRefusesTheConnect(t *testing.T) {
	var calls atomic.Int32
	log := &debugLog{}
	d, _ := startGuard(t, debugProcessTable(func(context.Context) string {
		if calls.Add(1) == 1 {
			return "21 /opt/chrome --remote-debugging-port=9370\n"
		}
		return "hang"
	}), log.logf)
	time.Sleep(1500 * time.Millisecond)
	err := connectWithin(t, d, "127.0.0.1:9400", 15*time.Second)
	if err == nil || !strings.Contains(err.Error(), procListRefused) {
		t.Fatalf("a connect to this machine while the process scan hangs: %v", err)
	}
	log.waitFor(t, procListFailed, 3*time.Second)
}

func TestProcessListScanIdlesQuietly(t *testing.T) {
	var calls atomic.Int32
	startGuard(t, debugProcessTable(func(context.Context) string {
		calls.Add(1)
		return "21 /opt/chrome --remote-debugging-port=9370\n"
	}), t.Logf)
	time.Sleep(5 * time.Second)
	if n := calls.Load(); n > 2 {
		t.Fatalf("an idle node scanned the process list %d times in 5 s", n)
	}
}

func TestProcessListScanShutdownSkipsFailure(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	var once sync.Once
	log := &debugLog{}
	d, cancel := startGuard(t, debugProcessTable(func(context.Context) string {
		if calls.Add(1) == 1 {
			return "21 /opt/chrome --remote-debugging-port=9370\n"
		}
		once.Do(func() { close(started) })
		return "hang"
	}), log.logf)
	time.Sleep(1500 * time.Millisecond)
	waiting := connectAsync(d, "127.0.0.1:9400")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the connect never started a scan")
	}
	cancel()
	select {
	case err := <-waiting:
		if err == nil {
			t.Fatal("a connect waiting on a scan cancelled by shutdown went through")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a connect waiting on a scan never finished after shutdown")
	}
	time.Sleep(100 * time.Millisecond)
	if n := log.count("couldn't list running browsers"); n != 0 {
		t.Fatalf("reported a failure on shutdown: %v", log.lines)
	}
}
