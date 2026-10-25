package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCDPNativeLateAttachedFrozenIframe(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, port, _ := strings.Cut(r.Host, ":")
		if r.URL.Path == "/framed" {
			_, _ = fmt.Fprintf(w, `<!doctype html><body><iframe src="http://seven.guard.test:%s/child"></iframe>`, port)
		} else {
			_, _ = fmt.Fprint(w, `<!doctype html><body><p>fixture</p>`)
		}
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://ordinary.outer.test:" + port
	protected := "http://seven.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--site-per-process", "--host-resolver-rules=MAP *.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "frame-generation-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("frame generation browser missing")
	}
	opts := map[string]any{"browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "ordinary": ordinary, "protected": protected}
	var raw json.RawMessage
	env.p.evalWithin("linkTest.frameGeneration("+jsArgs(opts)+")", &raw, time.Minute)
	path := filepath.Join("..", "..", "artifacts", "frame-generation")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "native.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("native ownership: %s", raw)
	var row struct {
		Tree          cdpOutcome   `json:"tree"`
		DOM           cdpOutcome   `json:"dom"`
		Shot          cdpOutcome   `json:"shot"`
		Input         cdpOutcome   `json:"input"`
		RestoredOwner cdpOutcome   `json:"restoredOwner"`
		Back          []cdpOutcome `json:"back"`
		MS            int          `json:"ms"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if !row.Tree.OK || !row.DOM.OK || !row.Shot.OK || !row.Input.OK || !row.RestoredOwner.OK || row.MS >= 5000 || len(row.Back) != 4 {
		t.Fatalf("ordinary/restore ownership: %s", raw)
	}
	for _, r := range row.Back {
		if r.OK || !strings.Contains(r.Error.Message, "slicc: refused: target is on seven's origin") {
			t.Errorf("restored document command escaped: %+v", r)
		}
	}
}

func TestCDPIframeProtectionFollowsRootDocument(t *testing.T) {
	for _, mode := range []string{"known", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(seven, ordinary string) fakeScript {
				base := guardWaitScript("{}")
				loader := "old"
				frame := func() string {
					return fmt.Sprintf(`{"id":"T1","loaderId":%q,"url":%q,"securityOrigin":%q}`, loader, ordinary+"/fixture", ordinary)
				}
				commit := func(kind string) []string {
					var events []string
					for _, session := range []string{"S1", "S2"} {
						events = append(events, fmt.Sprintf(`{"method":"Page.frameNavigated","sessionId":%q,"params":{"type":%q,"frame":%s}}`, session, kind, frame()))
					}
					return events
				}
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						var events []string
						if mode == "known" {
							events = commit("Navigation")
						}
						events = append(events, fmt.Sprintf(`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":{"targetId":"CHILD","type":"iframe","url":%q,"parentFrameId":"T1"}}}`, seven+"/child"))
						return "{}", events
					case "Browser.getWindowBounds":
						loader = "new"
						return "{}", commit("Navigation")
					case "Browser.getWindowForTarget":
						loader = "old"
						return "{}", commit("BackForwardCacheRestore")
					case "Page.getFrameTree":
						return `{"frameTree":{"frame":` + frame() + `}}`, nil
					case "DOM.getDocument":
						return `{"root":{"nodeId":1}}`, nil
					case "Page.captureScreenshot":
						return `{"data":"Zml4dHVyZQ=="}`, nil
					}
					return base(method, params)
				}
			})
			var result struct {
				Before []cdpOutcome `json:"before"`
				Next   []cdpOutcome `json:"next"`
				Back   []cdpOutcome `json:"back"`
				MS     int          `json:"ms"`
			}
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.frameGenerationSequence('chrome-2', %t)", mode == "known"), &result)
			refused := "slicc: refused: target is on seven's origin"
			check := func(label string, results []cdpOutcome, allowed bool) {
				t.Helper()
				if len(results) != 4 {
					t.Fatalf("%s: missing command outcomes: %+v", label, results)
				}
				for _, r := range results {
					if r.OK != allowed || (!allowed && !strings.Contains(r.Error.Message, refused)) {
						t.Errorf("%s allowed=%t: %+v", label, allowed, r)
					}
				}
			}
			check("original", result.Before, false)
			if mode == "known" {
				check("next", result.Next, true)
				check("restored", result.Back, false)
				if result.MS >= 5000 {
					t.Errorf("document transition exceeded bound: %d ms", result.MS)
				}
			}
			counts := map[string]int{}
			for _, raw := range fake.received() {
				var m struct {
					Method  string `json:"method"`
					Session string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				if m.Session == "S2" {
					counts[m.Method]++
				}
			}
			want := 0
			if mode == "known" {
				want = 1
			}
			for _, method := range []string{"Page.getFrameTree", "DOM.getDocument", "Page.captureScreenshot", "Input.dispatchMouseEvent"} {
				if counts[method] != want {
					t.Errorf("%s reached browser %d times, want %d", method, counts[method], want)
				}
			}
		})
	}
}
