package integration

import (
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
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
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
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, fakeDebugBody) }), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
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
