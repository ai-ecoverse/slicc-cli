package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

func TestLastLaunchRoundTrip(t *testing.T) {
	root := t.TempDir()
	o := browser.Options{ProfilesDir: filepath.Join(root, "slicc", "seven-profiles")}
	if got := browser.LoadLast(o); got != (browser.Last{}) {
		t.Fatalf("nothing saved yet: %+v", got)
	}
	want := browser.Last{ID: "chrome", Profile: "work"}
	if err := browser.SaveLast(o, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "slicc", "last-launch.json")); err != nil {
		t.Fatal(err)
	}
	if got := browser.LoadLast(o); got != want {
		t.Fatalf("loaded %+v", got)
	}
	if err := browser.SaveLast(browser.Options{}, want); err != nil {
		t.Fatal(err)
	}
	if got := browser.LoadLast(browser.Options{}); got != (browser.Last{}) {
		t.Fatalf("isolated discovery read a last launch: %+v", got)
	}
}

func TestTUILastLaunchedComesFirst(t *testing.T) {
	h := newLastTUI(t, "", browser.Last{ID: "chrome", Profile: "work"})
	rows := h.rows()
	for _, s := range []string{"›", "Google Chrome ", "141", `profile "work"`, "last launched"} {
		if !strings.Contains(rows[0], s) {
			t.Fatalf("row 0 %q lacks %q:\n%s", rows[0], s, h.view())
		}
	}
	if v := h.view(); strings.Contains(v, "best match") || !strings.Contains(rows[2], ":9222") || strings.Index(v, "LAUNCH") > strings.Index(v, "RUNNING") {
		t.Fatalf("rows:\n%s", h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].ID != "chrome" || h.started[0].Profiles["chrome"] != "work" {
		t.Fatalf("started %+v", h.started)
	}

	h = newLastTUI(t, "", browser.Last{ID: "chrome", Profile: "gone"})
	if r := h.rows()[0]; !strings.Contains(r, `profile "chrome"`) || strings.Contains(r, "work") {
		t.Fatalf("a deleted profile came back: %q", h.rows()[0])
	}
	h = newLastTUI(t, "", browser.Last{ID: "brave", Profile: "brave"})
	if !strings.Contains(h.rows()[0], "best match") {
		t.Fatalf("an uninstalled last launch moved rows:\n%s", h.view())
	}
}

func headlessCDP(t *testing.T, ua string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/156.0.8078.4",
			"User-Agent":           ua,
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/h",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestHeadlessEndpointIsNamedByItsApp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the process table is CIM JSON on Windows")
	}
	cft := "/Users/x/Library/Caches/ms-playwright/chromium-1248/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
	cases := []struct{ exe, ua, flags, app string }{
		{cft, "Mozilla/5.0 (Macintosh) HeadlessChrome/156.0.0.0 Safari/537.36", "", "Google Chrome for Testing"},
		{cft, "Mozilla/5.0 (Macintosh) Chrome/156.0.0.0 Safari/537.36", "--headless ", "Google Chrome for Testing"},
		{"/Users/x/Library/Caches/ms-playwright/chromium-1248/chrome-mac-arm64/Google Chrome for Testing.app/Contents/Frameworks/Google Chrome for Testing Framework.framework/Versions/156.0.8078.4/Helpers/Google Chrome for Testing Helper.app/Contents/MacOS/Google Chrome for Testing Helper", "Mozilla/5.0 HeadlessChrome/156.0.0.0", "", "Google Chrome for Testing"},
		{"/Users/x/Library/Caches/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-mac-arm64/chrome-headless-shell", "Mozilla/5.0 HeadlessChrome/156.0.0.0", "", "chrome-headless-shell"},
		{"/Applications/Slack.app/Contents/MacOS/Slack", "Mozilla/5.0 Chrome/140.0.0.0 Electron/38.0.0", "--headless ", "Slack"},
		{"/opt/google/chrome/chrome", "Mozilla/5.0 HeadlessChrome/156.0.0.0", "", ""},
	}
	for _, c := range cases {
		port := headlessCDP(t, c.ua)
		table := []byte(fmt.Sprintf("4242 %s %s--remote-debugging-port=%d --user-data-dir=/tmp/x about:blank\n", c.exe, c.flags, port))
		eps, _ := browser.DiscoverEndpoints(context.Background(), browser.Options{Processes: func(context.Context) ([]byte, error) { return table, nil }})
		if len(eps) != 1 || !eps[0].Headless() {
			t.Fatalf("%s %q: endpoints %+v", c.exe, c.flags, eps)
		}
		if eps[0].App != c.app {
			t.Fatalf("%s: app %q, want %q", c.exe, eps[0].App, c.app)
		}
		if b, _ := json.Marshal(eps[0]); c.app != "" && strings.Contains(string(b), c.app) {
			t.Fatalf("the app name leaked into the ranking JSON: %s", b)
		}
	}
}

func TestTUIColorsHeadlessAndLastLaunched(t *testing.T) {
	h := &tuiHarness{t: t, now: time.Now()}
	h.m = tui.New(tui.Config{Entries: headlessEntries(), Color: true, Last: browser.Last{ID: "chrome", Profile: "chrome"}})
	h.keys("h")
	rows := h.rows()
	green := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*92[;m]`)
	faint := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*2[;m]Google Chrome for Testing`)
	if !green.MatchString(rows[0]) || !strings.Contains(rows[0], "last launched") {
		t.Fatalf("last launched row not green: %q", rows[0])
	}
	if !faint.MatchString(rows[1]) || green.MatchString(rows[1]) {
		t.Fatalf("headless row not dimmed: %q", rows[1])
	}
}

func headlessEntries() []browser.Entry {
	ep := browser.Endpoint{CDP: "http://127.0.0.1:9444", Name: "Chrome", App: "Google Chrome for Testing", Version: "156.0.8078.4", UserAgent: "Mozilla/5.0 HeadlessChrome/156.0.0.0", Source: browser.SourceProcess, SevenTabs: []browser.Tab{}}
	ins := []browser.Installed{
		{ID: "electron:slack", Name: "Slack", Engine: browser.EngineElectron, Path: "/Applications/Slack.app", Executable: "slack", DebugSupport: browser.DebugUnknown},
		{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "155.0.1.2", Path: "/g", Executable: "g", DebugSupport: browser.DebugSupported},
	}
	return browser.Rank([]browser.Endpoint{ep}, ins)
}

func TestHeadlessEndpointsAreLowPriority(t *testing.T) {
	entries := headlessEntries()
	if entries[0].ID != "cdp:127.0.0.1:9444" {
		t.Fatalf("the ranking itself changed: %v", ids(entries))
	}
	if got := ids(flow.Demoted(entries)); strings.Join(got, ",") != "chrome,cdp:127.0.0.1:9444,electron:slack" {
		t.Fatalf("demoted %v", got)
	}
	if e, err := flow.Default(entries); err != nil || e.ID != "cdp:127.0.0.1:9444" {
		t.Fatalf("--yes no longer follows the ranking: %v %v", e.ID, err)
	}
	flagged := slices.Clone(entries)
	ep := *flagged[0].Endpoint
	ep.Source = browser.SourceFlag
	flagged[0].Endpoint = &ep
	if got := ids(flow.Demoted(flagged)); got[0] != "cdp:127.0.0.1:9444" {
		t.Fatalf("a --cdp endpoint was demoted: %v", got)
	}

	h := newSixTUI(t, entries)
	rows := h.rows()
	if len(rows) != 1 || !strings.Contains(rows[0], "Google Chrome ") || !strings.Contains(rows[0], "155") || !strings.Contains(rows[0], "best match") || !strings.Contains(h.view(), "▸ 1 headless browser") {
		t.Fatalf("rows:\n%s", h.view())
	}
	h.keys("h")
	rows = h.rows()
	if len(rows) != 2 || !strings.Contains(rows[1], "Google Chrome for Testing") || !strings.Contains(rows[1], "headless") || !strings.Contains(rows[1], ":9444") || strings.Contains(rows[1], "best match") {
		t.Fatalf("headless row:\n%s", h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].ID != "chrome" {
		t.Fatalf("started %+v", h.started)
	}
}

func TestTUILastLaunchedPrefersItsLiveEndpoint(t *testing.T) {
	dir := t.TempDir()
	ep := browser.Endpoint{CDP: "http://127.0.0.1:9555", Name: "Google Chrome", Version: "141.0.7390.1", Source: browser.SourceActivePort, UserDataDir: filepath.Join(dir, "work"), SevenTabs: []browser.Tab{}}
	entries := browser.Rank([]browser.Endpoint{ep}, []browser.Installed{
		{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "141.0.7390.1", Path: "/g", Executable: "g", DebugSupport: browser.DebugSupported},
	})
	h := &tuiHarness{t: t, now: time.Now()}
	h.m = tui.New(tui.Config{
		Entries:  entries,
		Last:     browser.Last{ID: "chrome", Profile: "work"},
		Profiles: func(browser.Entry) []string { return []string{"work"} },
		Dir:      func(_ browser.Entry, name string) string { return filepath.Join(dir, name) },
		Start:    func(c tui.Choice) { h.started = append(h.started, c) },
	})
	rows := h.rows()
	if !strings.Contains(rows[0], ":9555") || !strings.Contains(rows[0], "last launched") || strings.Contains(rows[1], "last launched") {
		t.Fatalf("rows:\n%s", h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].ID != "cdp:127.0.0.1:9555" {
		t.Fatalf("started %+v", h.started)
	}
}

func TestTUIKeepsAHeadlessCDPFlagInRunning(t *testing.T) {
	entries := headlessEntries()
	ep := *entries[0].Endpoint
	ep.Source = browser.SourceFlag
	entries[0].Endpoint = &ep
	h := newSixTUI(t, entries)
	rows := h.rows()
	if !strings.Contains(rows[0], ":9444") || !strings.Contains(rows[0], "best match") || !strings.Contains(h.view(), "Running") || strings.Contains(h.view(), "headless browser") {
		t.Fatalf("rows:\n%s", h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].ID != "cdp:127.0.0.1:9444" {
		t.Fatalf("started %+v", h.started)
	}
}

func TestTUIFoldKeysKeepTheHighlightedRow(t *testing.T) {
	h := newSixTUI(t, headlessEntries())
	h.keys("e", "down", "down", "down")
	if !strings.Contains(h.view(), "Adds Slack as an extra browser with space") {
		t.Fatalf("cursor not on Slack:\n%s", h.view())
	}
	h.keys("h")
	if !strings.Contains(h.view(), "Adds Slack as an extra browser") {
		t.Fatalf("opening the headless fold above moved the highlight:\n%s", h.view())
	}
	h.keys("h")
	if !strings.Contains(h.view(), "Adds Slack as an extra browser") {
		t.Fatalf("closing the headless fold above moved the highlight:\n%s", h.view())
	}
	h.keys("e")
	if !strings.Contains(h.view(), "↵ Shows 1 Electron app") {
		t.Fatalf("closing the fold the cursor is in:\n%s", h.view())
	}
}
