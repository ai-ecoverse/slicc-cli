package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const pdfViewerPage = "chrome-extension://mhjfbmdgcfjbbpaeojofohoefgiehjai/index.html"

var privilegedURLs = []string{
	"chrome://settings/",
	"chrome://password-manager/",
	"CHROME://Settings/passwords",
	"chrome-untrusted://print/",
	"chrome-untrusted://new-tab-page.evil/",
	pdfViewerPage,
	"file:///",
	"devtools://devtools/bundled/inspector.html",
	"edge://settings/passwords",
	"view-source:chrome://settings/",
	"view-source:" + pdfViewerPage,
	"view-source:file:///",
	"about:settings",
	"about:password-manager",
}

func TestCDPProtectsPrivilegedBrowserPages(t *testing.T) {
	env := setupProtect(t)
	t.Run("navigate and createTarget are refused", env.testPrivilegedRefused)
	t.Run("a privileged tab is redacted and can't be attached", env.testPrivilegedTabs)
	t.Run("browser_ui targets are hidden and refused", env.testBrowserUI)
	t.Run("the link still works", env.testStillLinked)
}

func (e *protectEnv) unguardedCall(t *testing.T, method string, params, result any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := e.b.client.Call(ctx, "", method, params, result); err != nil {
		t.Fatalf("unguarded %s: %v", method, err)
	}
}

func (e *protectEnv) testPrivilegedRefused(t *testing.T) {
	before := pageTargets(t, e.b)
	var r struct {
		Navigate map[string]struct {
			cdpOutcome
			Href string `json:"href"`
		} `json:"navigate"`
		Create map[string]cdpOutcome `json:"create"`
	}
	e.evalJSON(t, "linkTest.protect.privileged("+jsArgs("chrome", privilegedURLs)+")", &r)
	for _, u := range privilegedURLs {
		nav, ok := r.Navigate[u]
		want := refusedText
		if strings.HasPrefix(u, "about:") {
			want = refusedInheritedText
		}
		if !ok || !nav.refused() || nav.Error.Message != want || nav.Href != "about:blank" {
			t.Errorf("Page.navigate %s: %+v", u, nav)
		}
		if c := r.Create[u]; !c.refused() || c.Error.Message != refusedText {
			t.Errorf("Target.createTarget %s: %+v", u, c)
		}
	}
	for id, u := range pageTargets(t, e.b) {
		if _, ok := before[id]; !ok && u != "about:blank" {
			t.Errorf("a refused call left a page at %s", u)
		}
	}
	var ok struct {
		Navigate map[string]struct {
			cdpOutcome
			Href string `json:"href"`
		} `json:"navigate"`
		Create map[string]cdpOutcome `json:"create"`
	}
	allowed := []string{"about:blank#ok", "chrome://new-tab-page/", e.allowed + "/?n=privileged"}
	e.evalJSON(t, "linkTest.protect.privileged("+jsArgs("chrome", allowed)+")", &ok)
	for _, u := range allowed {
		if strings.HasPrefix(u, "about:") {
			if !ok.Navigate[u].refused() || ok.Navigate[u].Error.Message != refusedInheritedText || !ok.Create[u].OK {
				t.Errorf("inheriting navigation was not refused or a fresh opaque tab was unavailable: %s %+v %+v", u, ok.Navigate[u], ok.Create[u])
			}
			continue
		}
		if !ok.Navigate[u].OK || !ok.Create[u].OK {
			t.Errorf("%s is not privileged, but navigate gave %+v and createTarget %+v", u, ok.Navigate[u], ok.Create[u])
		}
	}
}

func (e *protectEnv) testPrivilegedTabs(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "credentials.txt")
	if err := os.WriteFile(secret, []byte("fake-value-0001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, shown string }{
		{"chrome://settings/", "chrome://settings"},
		{fileURL(secret), "file:"},
	} {
		var created struct {
			TargetID string `json:"targetId"`
		}
		e.unguardedCall(t, "Target.createTarget", map[string]any{"url": tc.url}, &created)
		waitTargetURL(t, e.b, created.TargetID, tc.url)
		var listed *protectTarget
		for _, tg := range e.targets(t) {
			if tg.TargetID == created.TargetID {
				listed = &tg
			}
		}
		if listed == nil || listed.URL != tc.shown || listed.Title != tc.shown {
			t.Errorf("the %s tab is listed as %+v, want its URL and title shown as %s", tc.url, listed, tc.shown)
		}
		var r map[string]json.RawMessage
		e.evalJSON(t, "linkTest.protect.attach("+jsArgs("chrome", created.TargetID)+")", &r)
		for _, m := range []string{"Target.attachToTarget", "Target.activateTarget", "Target.closeTarget", "Browser.getWindowForTarget"} {
			var o cdpOutcome
			_ = json.Unmarshal(r[m], &o)
			if !o.refused() {
				t.Errorf("%s on the %s tab: %s", m, tc.url, r[m])
			}
		}
		e.unguardedCall(t, "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
	}
}

func waitTargetURL(t *testing.T, b *chromeProc, id, prefix string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		infos, err := b.client.Targets(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		for _, ti := range infos {
			if ti.TargetID == id && strings.HasPrefix(strings.ToLower(ti.URL), strings.ToLower(prefix)) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("target %s never reached %s", id, prefix)
}

func browserUITarget(t *testing.T, b *chromeProc) cdp.TargetInfo {
	t.Helper()
	var r struct {
		TargetInfos []cdp.TargetInfo `json:"targetInfos"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := b.client.Call(ctx, "", "Target.getTargets", map[string]any{}, &r)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		for _, ti := range r.TargetInfos {
			if ti.Type == "browser_ui" {
				return ti
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the test Chrome has no browser_ui target")
	return cdp.TargetInfo{}
}

func (e *protectEnv) testBrowserUI(t *testing.T) {
	ui := browserUITarget(t, e.b)
	var r struct {
		Listed       int        `json:"listed"`
		Discovered   int        `json:"discovered"`
		AutoAttached int        `json:"autoAttached"`
		Attach       cdpOutcome `json:"Target.attachToTarget"`
		Info         cdpOutcome `json:"Target.getTargetInfo"`
		Activate     cdpOutcome `json:"Target.activateTarget"`
		Window       cdpOutcome `json:"Browser.getWindowForTarget"`
	}
	e.evalJSON(t, "linkTest.protect.browserUI("+jsArgs("chrome", ui.TargetID)+")", &r)
	if r.Listed != 0 || r.Discovered != 0 || r.AutoAttached != 0 {
		t.Errorf("browser_ui %s (%s) reached the page: listed %d, targetCreated %d, attachedToTarget %d", ui.TargetID, ui.URL, r.Listed, r.Discovered, r.AutoAttached)
	}
	for name, o := range map[string]cdpOutcome{"Target.attachToTarget": r.Attach, "Target.getTargetInfo": r.Info, "Target.activateTarget": r.Activate, "Browser.getWindowForTarget": r.Window} {
		if !o.refused() || o.Error.Message != refusedText {
			t.Errorf("%s on browser_ui %s: %+v", name, ui.URL, o)
		}
	}
}
