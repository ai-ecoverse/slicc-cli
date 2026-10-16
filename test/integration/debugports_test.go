package integration

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	ranged   int
	exposed  int
	found    int
	foundExp int
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
	d := &debugPorts{set: set}
	d.ranged, d.exposed = fakeDebugPair(t)
	d.found, d.foundExp = fakeDebug(t), fakeDebug(t)
	for name, port := range map[string]int{"found": d.found, "exposed": d.foundExp} {
		dir := filepath.Join(config, "slicc", "seven-profiles", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/%s\n", port, name)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set[browser.DiscoveryEnv] = "home"
	set[browser.ProtectedPortsEnv] = fmt.Sprintf("%d-%d", d.ranged, d.exposed)
	return d
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
	return []string{"--allow", "127.0.0.1:" + strconv.Itoa(d.ranged), "--allow", "127.0.0.1:" + strconv.Itoa(d.found), "--allow", "127.0.0.0/8"}
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
	hosts := []string{"127.0.0.1"}
	if lan, ok := findLANAddress(); ok {
		hosts = append(hosts, lan.String())
	}
	for _, port := range append([]int{d.ranged, d.found}, alsoRefused...) {
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

func TestAttachDeniesTheHostsDebuggingPorts(t *testing.T) {
	d := newDebugPorts(t)
	env := setupLinkEnv(t, false, d.list(), append(d.allowArgs(), d.exposeArgs()...)...)
	d.check(t, env.p, env.b.port)
}

func TestDefaultDeniesTheHostsDebuggingPorts(t *testing.T) {
	d := newDebugPorts(t)
	b := launchBrowser(t)
	base, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/index.html"
	id := "cdp:127.0.0.1:" + strconv.Itoa(b.port)
	args := append([]string{"--plain", "--no-kernel", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--expose-debug-port", strconv.Itoa(b.port)}, d.allowArgs()...)
	startFlow(t, d.env(base), append(args, d.exposeArgs()...)...)
	d.check(t, dialPage(t, b.endpoint, pageURL), b.port)
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
}

func TestFollowDeniesTheHostsDebuggingPorts(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	d := newDebugPorts(t)
	args := append(append([]string{"--exit", "--allow", "127.0.0.1/32"}, d.allowArgs()...), d.exposeArgs()...)
	if lan, ok := findLANAddress(); ok {
		args = append(args, "--allow", lan.String())
	}
	fp := startFollow(t, d.env(env), h.joinURL(trays[0]), args...)
	fp.waitStderr(t, "exit on", 15*time.Second)
	p.waitUp(t, "")
	d.check(t, p)
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
