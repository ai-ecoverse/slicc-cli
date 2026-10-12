package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

const secretMark = "s3cret"

func secretPages() []map[string]string {
	return []map[string]string{
		{"id": "P1", "type": "page", "url": "https://lars.sliccy.ai/", "title": "seven", "webSocketDebuggerUrl": "ws://127.0.0.1/devtools/page/P1"},
		{"id": "S3CRET-ID-77", "type": "page", "url": "https://s3cret-intranet.example.com/s3cret-inbox?token=S3CRET-TOKEN-a9f2", "title": "S3CRET payroll Q4", "webSocketDebuggerUrl": "ws://127.0.0.1/devtools/page/S3CRET-ID-77"},
		{"id": "S2", "type": "page", "url": "https://www.sliccy.ai/s3cret-www", "title": "S3CRET www"},
		{"id": "S3", "type": "iframe", "url": "https://frame.sliccy.ai/", "title": "S3CRET frame"},
		{"id": "S4", "type": "service_worker", "url": "https://lars.sliccy.ai/sw.js", "title": "S3CRET worker"},
		{"id": "S5", "type": "page", "url": "file:///Users/me/s3cret-diary.txt", "title": "S3CRET diary"},
		{"id": "S6", "type": "page", "url": "https://lars.sliccy.ai:8443/s3cret-port", "title": "S3CRET other port"},
	}
}

type recordingCDP struct {
	*httptest.Server
	port     int
	wsPath   string
	redirect string
	mu       sync.Mutex
	requests []string
	conns    atomic.Int32
}

func newRecordingCDP(t *testing.T) *recordingCDP {
	t.Helper()
	f := &recordingCDP{wsPath: "/devtools/browser/rec-" + strconv.Itoa(int(nextRecorder.Add(1)))}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			f.conns.Add(1)
		}
	}
	f.Start()
	t.Cleanup(f.Close)
	f.port = f.Listener.Addr().(*net.TCPAddr).Port
	return f
}

var nextRecorder atomic.Int32

func (f *recordingCDP) serve(w http.ResponseWriter, r *http.Request) {
	line := r.Method + " " + r.URL.RequestURI()
	if r.Header.Get("Upgrade") != "" {
		line += " upgrade " + r.Header.Get("Upgrade")
	}
	f.mu.Lock()
	f.requests = append(f.requests, line)
	redirect := f.redirect
	f.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/json/version":
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/154.0.8037.98",
			"Protocol-Version":     "1.3",
			"User-Agent":           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.8037.98 Safari/537.36",
			"webSocketDebuggerUrl": "ws://" + r.Host + f.wsPath,
		})
	case "/json/list":
		_ = json.NewEncoder(w).Encode(secretPages())
	default:
		http.NotFound(w, r)
	}
}

func (f *recordingCDP) redirectTo(u string) {
	f.mu.Lock()
	f.redirect = u
	f.mu.Unlock()
}

func (f *recordingCDP) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *recordingCDP) id() string { return "cdp:127.0.0.1:" + strconv.Itoa(f.port) }

func (f *recordingCDP) activePort() string { return fmt.Sprintf("%d\n%s\n", f.port, f.wsPath) }

func (f *recordingCDP) requireReadOnly(t *testing.T, what string) {
	t.Helper()
	got := f.got()
	seen := map[string]bool{}
	for _, r := range got {
		if r != "GET /json/version" && r != "GET /json/list" {
			t.Fatalf("%s: discovery sent %q to a browser; only GET /json/version and GET /json/list are allowed (all: %q)", what, r, got)
		}
		seen[r] = true
	}
	if !seen["GET /json/version"] || !seen["GET /json/list"] {
		t.Fatalf("%s: the fake browser was not discovered: %q", what, got)
	}
}

func requireNoSecret(t *testing.T, what, text string) {
	t.Helper()
	if i := strings.Index(strings.ToLower(text), secretMark); i >= 0 {
		lo, hi := max(0, i-80), min(len(text), i+80)
		t.Fatalf("%s leaked a tab that isn't seven: …%s…", what, text[lo:hi])
	}
}

func requireNoSecretFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		requireNoSecret(t, "the file "+path, string(b)+path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type guardHome struct {
	fakeHome
	env []string
}

func newGuardHome(t *testing.T, extra ...string) guardHome {
	t.Helper()
	env := isolatedEnv(t, extra...)
	root := envMap(env)["HOME"]
	env = overrideEnv(env, map[string]string{
		"LOCALAPPDATA":    filepath.Join(root, "Local"),
		"APPDATA":         filepath.Join(root, "Roaming"),
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(root, ".cache"),
	})
	return guardHome{fakeHome: fakeHome{root: root, env: env}, env: env}
}

func writeActivePort(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, browser.ActivePortFile), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryNeverLeaksTabsThatArentSeven(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	h := newGuardHome(t, append(col.env(), "SLICC_DEBUG=1", "SLICC_LOG_LEVEL=debug", "SLICC_NO_UPDATE_CHECK=1")...)
	personal := newRecordingCDP(t)
	own := newRecordingCDP(t)
	flagged := newRecordingCDP(t)
	writeActivePort(t, h.chromeDataDir(), personal.activePort())
	writeActivePort(t, filepath.Join(h.profilesDir(), "own"), own.activePort())
	home := withDiscovery(h.env, "home")

	runs := [][]string{
		{"browsers", "--cdp", flagged.URL},
		{"browsers", "--json", "--cdp", flagged.URL},
		{"--json", "--cdp", flagged.URL, "--browser", "no-such-browser"},
		{"--plain", "--cdp", flagged.URL, "--browser", "no-such-browser"},
	}
	for _, args := range runs {
		r := runBin(t, home, bin, args...)
		what := strings.Join(args, " ")
		requireNoSecret(t, what+" stdout", r.stdout)
		requireNoSecret(t, what+" stderr", r.stderr)
		if strings.Contains(what, "json") && (!strings.Contains(r.stdout, "https://lars.sliccy.ai/") || strings.Count(r.stdout, `"sevenOpen":`) < 3) {
			t.Fatalf("%s: the seven tabs are missing: %+v", what, r)
		}
	}
	for name, f := range map[string]*recordingCDP{"personal": personal, "own": own, "flagged": flagged} {
		f.requireReadOnly(t, name)
	}

	attachTo := newRecordingCDP(t)
	r := runBin(t, home, bin, "--json", "--cdp", attachTo.URL, "--browser", attachTo.id())
	if r.code != 1 || !strings.Contains(r.stdout, `"event":"error"`) {
		t.Fatalf("attaching to a fake without a WebSocket: %+v", r)
	}
	requireNoSecret(t, "a failed attach's stdout", r.stdout)
	requireNoSecret(t, "a failed attach's stderr", r.stderr)

	beacons := col.got()
	if col.find("enter") == nil || col.find("error") == nil {
		t.Fatalf("telemetry was not live: %+v", beacons)
	}
	for _, b := range beacons {
		requireNoSecret(t, "a telemetry beacon", b.raw)
	}
	requireNoSecretFiles(t, h.root)
}

func TestDiscoveryReadsOnlyVersionAndListInEverySource(t *testing.T) {
	home := t.TempDir()
	h := fakeHome{root: home}
	personal := newRecordingCDP(t)
	own := newRecordingCDP(t)
	flagged := newRecordingCDP(t)
	probed := newRecordingCDP(t)
	scanned := newRecordingCDP(t)
	scannedZero := newRecordingCDP(t)
	writeActivePort(t, userDataDir(h), personal.activePort())
	profiles := filepath.Join(home, "profiles")
	writeActivePort(t, filepath.Join(profiles, "own"), own.activePort())
	zeroDir := filepath.Join(home, "pw")
	writeActivePort(t, zeroDir, scannedZero.activePort())
	table := processTable([][2]any{
		{4242, "/opt/google/chrome/chrome --remote-debugging-port=" + strconv.Itoa(scanned.port) + " --user-data-dir=" + filepath.Join(home, "scanned")},
		{4243, "/opt/google/chrome/chrome --remote-debugging-port=0 --user-data-dir=" + zeroDir},
	})
	o := browser.Options{
		Home: home, Getenv: fakeGetenv(home), ProfilesDir: profiles, CDPURLs: []string{flagged.URL},
		ProbePorts: []int{probed.port},
		Processes:  func(context.Context) ([]byte, error) { return table, nil },
	}
	r := browser.Discover(context.Background(), o)
	sources := map[string]*recordingCDP{"user-data dir": personal, "seven profile": own, "--cdp": flagged, "probe": probed, "process": scanned, "process, port 0": scannedZero}
	byID := map[string]browser.Entry{}
	for _, e := range r.Entries {
		byID[e.ID] = e
	}
	for name, f := range sources {
		f.requireReadOnly(t, name)
		e, ok := byID[f.id()]
		if !ok || e.Endpoint == nil || !e.Endpoint.SevenOpen || len(e.Endpoint.SevenTabs) != 1 || e.Endpoint.SevenTabs[0].ID != "P1" {
			t.Fatalf("%s: %+v", name, e.Endpoint)
		}
	}
	report, _ := json.Marshal(browser.NewReport(r.Entries))
	requireNoSecret(t, "the in-process report", string(report))
	requireNoSecret(t, "the in-process entries", fmt.Sprintf("%+v %+v %v", r.Endpoints, r.Entries, r.Errors))

	m := tea.Model(tui.New(tui.Config{Entries: r.Entries, Start: func(tui.Choice) {}, Stop: func() {}}))
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeySpace, Text: " "}} {
		m, _ = m.Update(k)
		requireNoSecret(t, "the picker", m.View().Content)
	}

	capProbe := newRecordingCDP(t)
	ep, err := browser.ProbeEndpoint(context.Background(), capProbe.URL, browser.Options{NoProbe: true})
	if err != nil || !ep.SevenOpen {
		t.Fatalf("the cdp capability's probe: %+v %v", ep, err)
	}
	capProbe.requireReadOnly(t, "the cdp capability's probe")
}

func TestDiscoveryFollowsNoRedirectsAndNoProxy(t *testing.T) {
	elsewhere := newRecordingCDP(t)
	redirecting := newRecordingCDP(t)
	redirecting.redirectTo(elsewhere.URL + "/json/new?https://example.com/")
	ep, err := browser.ProbeEndpoint(context.Background(), redirecting.URL, browser.Options{})
	if err == nil {
		t.Fatalf("a redirecting endpoint was listed: %+v", ep)
	}
	eps, _ := browser.DiscoverEndpoints(context.Background(), browser.Options{Home: t.TempDir(), ProbePorts: []int{redirecting.port}})
	if len(eps) != 0 {
		t.Fatalf("a redirecting probe port was listed: %+v", eps)
	}
	if got := elsewhere.got(); len(got) != 0 || elsewhere.conns.Load() != 0 {
		t.Fatalf("discovery followed a redirect: %q", got)
	}
	for _, r := range redirecting.got() {
		if r != "GET /json/version" {
			t.Fatalf("discovery sent %q after a redirect", r)
		}
	}

	proxy := newRecordingCDP(t)
	env := isolatedEnv(t, "HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=", "no_proxy=")
	res := runBin(t, env, binary, "browsers", "--json", "--cdp", "http://192.0.2.1:9400")
	if res.code != 0 || !strings.Contains(res.stderr, "192.0.2.1:9400") {
		t.Fatalf("browsers --cdp on an unroutable address: %+v", res)
	}
	if got := proxy.got(); len(got) != 0 || proxy.conns.Load() != 0 {
		t.Fatalf("discovery went through the proxy from the environment: %q", got)
	}
}

func TestTheDefaultFlowOffersOnlyTheBrowsersTheUserPicked(t *testing.T) {
	b := launchBrowser(t)
	personal := launchBrowser(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	p := b.openPage(t, pageURL)
	h := newFakeHome(t)
	writeActivePort(t, filepath.Join(h.profilesDir(), "it-seven"), activePortOf(t, b))
	writeActivePort(t, h.chromeDataDir(), activePortOf(t, personal))
	id := "cdp:127.0.0.1:" + strconv.Itoa(b.port)
	personalID := "cdp:127.0.0.1:" + strconv.Itoa(personal.port)

	f := startFlow(t, h.env, "--json", "--page", pageURL)
	d := f.next(t, "discovered")
	found := map[string]bool{}
	for _, e := range d.Entries {
		found[e.ID] = e.Endpoint != nil
	}
	if !found[id] || !found[personalID] {
		t.Fatalf("discovery should find both browsers: %s", d.raw)
	}
	if a := f.next(t, "attached"); a.Browser != id || a.Role != "seven" || a.Launched {
		t.Fatalf("attached %s", a.raw)
	}
	f.next(t, "linked")
	var hello struct {
		Browsers []struct {
			ID    string `json:"id"`
			Seven bool   `json:"seven"`
		} `json:"browsers"`
	}
	p.waitFor("linkTest.status().up", 30*time.Second)
	p.eval("linkTest.hello()", &hello)
	if len(hello.Browsers) != 1 || !hello.Browsers[0].Seven {
		t.Fatalf("HELLO browsers %+v: only the browser seven runs in may be offered", hello.Browsers)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	f.next(t, "link-lost")
	if c := f.next(t, "closed"); len(c.Browsers) != 0 {
		t.Fatalf("closed %s", c.raw)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func activePortOf(t *testing.T, b *chromeProc) string {
	t.Helper()
	v, err := getVersion(b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := v["webSocketDebuggerUrl"].(string)
	u, err := url.Parse(ws)
	if err != nil || u.Path == "" {
		t.Fatalf("webSocketDebuggerUrl %q", ws)
	}
	return fmt.Sprintf("%d\n%s\n", b.port, u.Path)
}

func userDataDir(h fakeHome) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h.root, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		return filepath.Join(h.root, "AppData", "Local", "Google", "Chrome", "User Data")
	default:
		return filepath.Join(h.root, ".config", "google-chrome")
	}
}

func fakeGetenv(home string) func(string) string {
	return func(k string) string {
		switch k {
		case "LOCALAPPDATA":
			return filepath.Join(home, "AppData", "Local")
		case "APPDATA":
			return filepath.Join(home, "AppData", "Roaming")
		}
		return ""
	}
}

func processTable(rows [][2]any) []byte {
	if runtime.GOOS == "windows" {
		var list []map[string]any
		for _, r := range rows {
			list = append(list, map[string]any{"ProcessId": r[0], "CommandLine": r[1]})
		}
		b, _ := json.Marshal(list)
		return b
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%d %s\n", r[0], r[1])
	}
	return []byte(b.String())
}
