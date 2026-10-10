package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

type fakeCDP struct {
	*httptest.Server
	wsPath string
	port   int
}

func newFakeCDP(t *testing.T, product string, pages []map[string]string) *fakeCDP {
	t.Helper()
	f := &fakeCDP{wsPath: "/devtools/browser/" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              product,
			"Protocol-Version":     "1.3",
			"User-Agent":           "Mozilla/5.0 " + strings.Replace(product, "HeadlessChrome", "Chrome", 1) + " Safari/537.36",
			"webSocketDebuggerUrl": "ws://" + r.Host + f.wsPath,
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(pages)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	f.port = f.Listener.Addr().(*net.TCPAddr).Port
	return f
}

func (f *fakeCDP) activePort() string {
	return fmt.Sprintf("%d\n%s\n", f.port, f.wsPath)
}

func sevenPages() []map[string]string {
	return []map[string]string{
		{"id": "A", "type": "page", "url": "https://lars.sliccy.ai/?x=1", "title": "seven"},
		{"id": "B", "type": "page", "url": "https://www.sliccy.ai/", "title": "www"},
		{"id": "C", "type": "iframe", "url": "https://other.sliccy.ai/", "title": "frame"},
		{"id": "D", "type": "page", "url": "https://example.com/", "title": "example"},
	}
}

type fakeHome struct {
	root string
	env  []string
}

func newFakeHome(t *testing.T) fakeHome {
	t.Helper()
	root := t.TempDir()
	h := fakeHome{root: root}
	h.env = overrideEnv(os.Environ(), map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"LOCALAPPDATA":    filepath.Join(root, "Local"),
		"APPDATA":         filepath.Join(root, "Roaming"),
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"SLICC_DISCOVERY": "home",
	})
	return h
}

func (h fakeHome) options() browser.Options {
	m := envMap(h.env)
	return browser.Options{Home: h.root, Getenv: func(k string) string { return m[k] }, NoProbe: true}
}

func (h fakeHome) chromeDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h.root, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		return filepath.Join(h.root, "Local", "Google", "Chrome", "User Data")
	default:
		return filepath.Join(h.root, ".config", "google-chrome")
	}
}

func (h fakeHome) edgeDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h.root, "Library", "Application Support", "Microsoft Edge")
	case "windows":
		return filepath.Join(h.root, "Local", "Microsoft", "Edge", "User Data")
	default:
		return filepath.Join(h.root, ".config", "microsoft-edge")
	}
}

func (h fakeHome) profilesDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h.root, "Library", "Application Support", "slicc", "profiles")
	case "windows":
		return filepath.Join(h.root, "Roaming", "slicc", "profiles")
	default:
		return filepath.Join(h.root, ".config", "slicc", "profiles")
	}
}

func overrideEnv(base []string, set map[string]string) []string {
	var out []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[strings.ToUpper(k)]; ok {
			continue
		}
		if _, ok := set[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
		m[strings.ToUpper(k)] = v
	}
	return m
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func runWithEnv(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

type report struct {
	Schema      int             `json:"schema"`
	MinChromium int             `json:"minChromium"`
	Entries     []browser.Entry `json:"entries"`
}

func browsersJSON(t *testing.T, env []string, args ...string) (report, string) {
	t.Helper()
	out, errOut, code := runWithEnv(t, env, append([]string{"browsers", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("browsers --json: code %d, stderr %s", code, errOut)
	}
	var r report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("browsers --json output is not JSON: %v\n%s", err, out)
	}
	return r, errOut
}

func entryByCDP(r report, base string) *browser.Entry {
	for i := range r.Entries {
		if r.Entries[i].Kind == browser.KindEndpoint && r.Entries[i].Endpoint.CDP == base {
			return &r.Entries[i]
		}
	}
	return nil
}

func TestBrowsersFindsEndpointsFromActivePortFilesAndFlags(t *testing.T) {
	h := newFakeHome(t)
	seven := newFakeCDP(t, "Chrome/150.0.7000.1", sevenPages())
	plain := newFakeCDP(t, "HeadlessChrome/151.0.1.2", []map[string]string{{"id": "X", "type": "page", "url": "about:blank"}})
	stale := newFakeCDP(t, "Chrome/150.0.1.1", nil)
	writeFile(t, filepath.Join(h.chromeDataDir(), "DevToolsActivePort"), seven.activePort())
	writeFile(t, filepath.Join(h.edgeDataDir(), "DevToolsActivePort"), fmt.Sprintf("%d\n/devtools/browser/gone\n", stale.port))
	writeFile(t, filepath.Join(h.profilesDir(), "work", "DevToolsActivePort"), fmt.Sprintf("%d\n/devtools/browser/x\n", closedPort(t)))
	unreachable := fmt.Sprintf("http://127.0.0.1:%d", closedPort(t))

	r, errOut := browsersJSON(t, h.env, "--cdp", plain.URL, "--cdp", unreachable)
	if r.Schema != 1 || r.MinChromium != browser.MinChromiumMajor {
		t.Fatalf("schema %d minChromium %d", r.Schema, r.MinChromium)
	}
	if !strings.Contains(errOut, unreachable) {
		t.Errorf("no warning for the unreachable --cdp URL, stderr %q", errOut)
	}
	if entryByCDP(r, stale.URL) != nil {
		t.Errorf("a stale DevToolsActivePort was listed")
	}
	got := entryByCDP(r, seven.URL)
	if got == nil {
		t.Fatalf("no endpoint from DevToolsActivePort in %+v", r.Entries)
	}
	ep := got.Endpoint
	if got.ID != "cdp:127.0.0.1:"+strconv.Itoa(seven.port) || got.Tier != browser.TierSevenOpen || ep.Source != browser.SourceActivePort ||
		ep.Browser != "chrome" || got.Name != "Google Chrome" || got.Version != "150.0.7000.1" || ep.UserDataDir != h.chromeDataDir() {
		t.Errorf("active-port endpoint: %+v %+v", got, ep)
	}
	if !ep.SevenOpen || len(ep.SevenTabs) != 1 || ep.SevenTabs[0].ID != "A" || ep.WebSocketURL != "ws://"+seven.Listener.Addr().String()+seven.wsPath {
		t.Errorf("seven tabs: %+v", ep)
	}
	flagged := entryByCDP(r, plain.URL)
	if flagged == nil || flagged.Endpoint.Source != browser.SourceFlag || flagged.Endpoint.SevenOpen || flagged.Tier != browser.TierEndpoint ||
		flagged.Name != "Chrome (headless)" || len(flagged.Endpoint.SevenTabs) != 0 {
		t.Fatalf("flag endpoint: %+v", flagged)
	}
	if indexOf(r, got.ID) > indexOf(r, flagged.ID) {
		t.Errorf("the seven-open endpoint comes after a plain one: %q", ids(r.Entries))
	}
	assertTiersAscending(t, r.Entries)
}

func indexOf(r report, id string) int {
	for i, e := range r.Entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

func assertTiersAscending(t *testing.T, entries []browser.Entry) {
	t.Helper()
	for i := 1; i < len(entries); i++ {
		if entries[i].Tier < entries[i-1].Tier {
			t.Errorf("entry %d (%s, tier %d) after tier %d", i, entries[i].ID, entries[i].Tier, entries[i-1].Tier)
		}
	}
}

func TestBrowsersJSONShapeIsStable(t *testing.T) {
	h := newFakeHome(t)
	seven := newFakeCDP(t, "Chrome/150.0.7000.1", sevenPages())
	out, _, code := runWithEnv(t, h.env, "browsers", "--json", "--cdp", seven.URL)
	if code != 0 {
		t.Fatal(code)
	}
	var raw struct {
		Schema  int              `json:"schema"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw.Entries {
		for _, k := range []string{"id", "kind", "tier", "name"} {
			if _, ok := e[k]; !ok {
				t.Errorf("entry %v has no %q", e["id"], k)
			}
		}
		switch e["kind"] {
		case "endpoint":
			ep := e["endpoint"].(map[string]any)
			for _, k := range []string{"cdp", "webSocketDebuggerUrl", "name", "source", "sevenOpen", "sevenTabs"} {
				if _, ok := ep[k]; !ok {
					t.Errorf("endpoint %v has no %q", e["id"], k)
				}
			}
		case "installed":
			in := e["installed"].(map[string]any)
			for _, k := range []string{"id", "name", "engine", "path", "executable", "debugSupport"} {
				if _, ok := in[k]; !ok {
					t.Errorf("installed %v has no %q", e["id"], k)
				}
			}
		default:
			t.Errorf("unknown kind %v", e["kind"])
		}
	}
}

func TestBrowsersPlainOutput(t *testing.T) {
	h := newFakeHome(t)
	seven := newFakeCDP(t, "Chrome/150.0.7000.1", sevenPages())
	out, _, code := runWithEnv(t, h.env, "browsers", "--cdp", seven.URL)
	if code != 0 || !strings.HasPrefix(out, "ID ") || !strings.Contains(out, "running, seven open") || !strings.Contains(out, seven.URL) {
		t.Fatalf("code %d, output %q", code, out)
	}
	_, errOut, code := runWithEnv(t, h.env, "browsers", "extra")
	if code != 2 || !strings.Contains(errOut, "usage: slicc browsers") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestDiscoverEndpointsProbesPortsAndFiltersOldVersions(t *testing.T) {
	h := newFakeHome(t)
	modern := newFakeCDP(t, "Chrome/150.0.1.1", sevenPages())
	old := newFakeCDP(t, "Chrome/100.0.1.1", nil)
	o := h.options()
	o.NoProbe = false
	o.ProbePorts = []int{modern.port, old.port, closedPort(t)}
	eps, errs := browser.DiscoverEndpoints(context.Background(), o)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(eps) != 1 || eps[0].CDP != modern.URL || eps[0].Source != browser.SourceProbe || !eps[0].SevenOpen {
		t.Fatalf("%+v", eps)
	}
	writeFile(t, filepath.Join(h.chromeDataDir(), "DevToolsActivePort"), modern.activePort())
	eps, _ = browser.DiscoverEndpoints(context.Background(), o)
	if len(eps) != 1 || eps[0].Source != browser.SourceActivePort || eps[0].Browser != "chrome" {
		t.Fatalf("the DevToolsActivePort source should win over the probe: %+v", eps)
	}
}

func TestDiscoverEndpointsWebSocketOnly(t *testing.T) {
	h := newFakeHome(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	writeFile(t, filepath.Join(h.chromeDataDir(), "DevToolsActivePort"), fmt.Sprintf("%d\n/devtools/browser/abc\n", port))
	eps, _ := browser.DiscoverEndpoints(context.Background(), h.options())
	if len(eps) != 1 || !eps[0].WSOnly || eps[0].WebSocketURL != fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser/abc", port) || eps[0].Name != "Google Chrome" {
		t.Fatalf("%+v", eps)
	}
}

func TestRankOrder(t *testing.T) {
	eps := []browser.Endpoint{
		{CDP: "http://127.0.0.1:9223", Name: "Brave", Product: "Chrome/150.0"},
		{CDP: "http://127.0.0.1:9230", Name: "Chrome", Product: "Chrome/150.0", SevenOpen: true},
		{CDP: "http://127.0.0.1:9222", Name: "Chrome", Product: "Chrome/150.0"},
		{CDP: "http://127.0.0.1:9224", Name: "Old", Product: "Chrome/99.0"},
	}
	ins := []browser.Installed{
		{ID: "electron:slack", Name: "Slack", Engine: browser.EngineElectron},
		{ID: "chromium", Name: "Chromium", Engine: browser.EngineChromium, Version: "150.0"},
		{ID: "chrome-canary", Name: "Google Chrome Canary", Engine: browser.EngineChromium, Version: "152.0"},
		{ID: "brave", Name: "Brave Browser", Engine: browser.EngineChromium, Version: "1.84.1"},
		{ID: "edge", Name: "Microsoft Edge", Engine: browser.EngineChromium, Version: "150.0"},
		{ID: "chrome-beta", Name: "Google Chrome Beta", Engine: browser.EngineChromium, Version: "110.0"},
		{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "150.0"},
		{ID: "electron:discord", Name: "Discord", Engine: browser.EngineElectron},
	}
	want := []string{
		"cdp:127.0.0.1:9230", "cdp:127.0.0.1:9222", "cdp:127.0.0.1:9223",
		"chrome", "edge", "brave", "chrome-canary", "chromium",
		"electron:discord", "electron:slack",
	}
	for range 5 {
		got := ids(browser.Rank(eps, ins))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q\nwant %q", got, want)
		}
		ins = append(ins[1:], ins[0])
		eps = append(eps[1:], eps[0])
	}
	best, ok := browser.BestInstalled(browser.Rank(nil, ins))
	if !ok || best.ID != "chrome" {
		t.Errorf("best installed %q", best.ID)
	}
}

func ids(entries []browser.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func TestSevenOrigins(t *testing.T) {
	for origin, want := range map[string]bool{
		"https://lars.sliccy.ai": true,
		"https://www.sliccy.ai":  false,
		"https://sliccy.ai":      false,
		"http://lars.sliccy.ai":  false,
		"https://a.b.sliccy.ai":  false,
	} {
		if browser.IsSevenOrigin(origin, nil) != want {
			t.Errorf("%s", origin)
		}
	}
}
