package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type navContextOriginFresh struct {
	Label         string `json:"label"`
	ListedURL     string `json:"listedURL"`
	Attached      bool   `json:"attached"`
	AttachError   string `json:"attachError"`
	Evaluated     bool   `json:"evaluated"`
	Href          string `json:"href"`
	EvaluateError string `json:"evaluateError"`
}

func navContextOriginRefused(f navContextOriginFresh) bool {
	if f.Attached || f.Evaluated || f.AttachError != "slicc: refused: target is on seven's origin" {
		return false
	}
	if f.ListedURL == "" {
		return true
	}
	u, err := url.Parse(f.ListedURL)
	return err == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !strings.HasSuffix(f.ListedURL, "?") && !strings.HasSuffix(f.ListedURL, "#")
}

type navContextOriginOrder struct {
	Label        string   `json:"label"`
	Order        string   `json:"order"`
	CommitIndex  int      `json:"commitIndex"`
	ContextIndex int      `json:"contextIndex"`
	Trace        []string `json:"trace"`
}

func TestCDPNavContextOriginReleasesOnRealNavigation(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step launch: CI Chrome required for the nav-session default-context row")
		}
		t.Skip(harnessChromeNotice)
	}
	var mu sync.Mutex
	armedTarget, skipSession := "", ""
	rewritten := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/arm" {
			mu.Lock()
			q := r.URL.Query()
			switch {
			case q.Get("disarm") != "":
				armedTarget, skipSession = "", ""
			case q.Get("target") != "":
				armedTarget, skipSession = q.Get("target"), q.Get("skip")
			}
			count := rewritten
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(count)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>nav context %s</title><body>%s", r.URL.Path, r.URL.Path)
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://page.ordinary.test:" + port
	protected := "http://seven.protected.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	gate.transform = func(data []byte) []byte {
		if !strings.Contains(string(data), `"Runtime.executionContextCreated"`) {
			return data
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			return data
		}
		session, _ := m["sessionId"].(string)
		params, _ := m["params"].(map[string]any)
		context, _ := params["context"].(map[string]any)
		aux, _ := context["auxData"].(map[string]any)
		isDefault, _ := aux["isDefault"].(bool)
		frame, _ := aux["frameId"].(string)
		mu.Lock()
		defer mu.Unlock()
		if armedTarget == "" || session == "" || session == skipSession || !isDefault || frame != armedTarget {
			return data
		}
		context["origin"] = protected
		rewritten++
		out, err := json.Marshal(m)
		if err != nil {
			return data
		}
		return out
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "nav-context-origin-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step hello: browsers %+v", hello.Browsers)
	}
	opts := map[string]any{
		"browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "arm": fixture.URL + "/arm",
		"seven": protected + "/seven", "afterSeven": ordinary + "/after-seven",
		"first": ordinary + "/first", "marked": ordinary + "/marked", "second": ordinary + "/second",
	}
	var raw json.RawMessage
	env.p.evalWithin("linkTest.navContextOrigin("+jsArgs(opts)+")", &raw, 150*time.Second)
	var row struct {
		Steps        []json.RawMessage      `json:"steps"`
		SevenBefore  navContextOriginFresh  `json:"sevenBefore"`
		SevenOrder   navContextOriginOrder  `json:"sevenOrder"`
		SevenAfter   *navContextOriginFresh `json:"sevenAfter"`
		Control      bool                   `json:"control"`
		Rewritten    int                    `json:"rewritten"`
		ClientCut    bool                   `json:"clientCut"`
		MarkOrder    navContextOriginOrder  `json:"markOrder"`
		Marked       navContextOriginFresh  `json:"marked"`
		ReleaseOrder navContextOriginOrder  `json:"releaseOrder"`
		After        *navContextOriginFresh `json:"after"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("step result: %v\n%s", err, raw)
	}
	for _, s := range row.Steps {
		t.Logf("step %s", s)
	}
	for _, o := range []navContextOriginOrder{row.SevenOrder, row.MarkOrder, row.ReleaseOrder} {
		t.Logf("ci-proof order navigation=%s order=%s commitIndex=%d contextIndex=%d trace=%s", o.Label, o.Order, o.CommitIndex, o.ContextIndex, strings.Join(o.Trace, " > "))
	}
	sevenAfter, after := navContextOriginFresh{}, navContextOriginFresh{}
	if row.SevenAfter != nil {
		sevenAfter = *row.SevenAfter
	}
	if row.After != nil {
		after = *row.After
	}
	t.Logf("ci-proof seven-to-ordinary before=%+v released=%t after=%+v", row.SevenBefore, row.SevenAfter != nil, sevenAfter)
	t.Logf("ci-proof nav-mark rewritten=%d clientCut=%t marked=%+v released=%t after=%+v", row.Rewritten, row.ClientCut, row.Marked, row.After != nil, after)
	lines := guardLines(env.a)
	for _, line := range strings.Split(lines, "\n") {
		if strings.Contains(line, "default-context mark") || strings.Contains(line, "default context marks") || strings.Contains(line, "moved onto a protected origin") {
			t.Logf("guard: %s", line)
		}
	}
	fail := func(format string, args ...any) {
		t.Fatalf(format+"\nci-proof orders: seven-to-ordinary=%s to-marked=%s marked-to-ordinary=%s\n%s\nguard:\n%s", append(args, row.SevenOrder.Order, row.MarkOrder.Order, row.ReleaseOrder.Order, raw, lines)...)
	}
	switch {
	case row.SevenOrder.Order == "context-before-commit" || row.ReleaseOrder.Order == "context-before-commit":
		fail("step order: context-before-commit: Chrome delivered the new document's default context before its Page.frameNavigated")
	case row.SevenOrder.Order != "commit-then-context" || row.ReleaseOrder.Order != "commit-then-context":
		fail("step order: the observer did not see both the commit and the new default context of each navigation")
	case !navContextOriginRefused(row.SevenBefore):
		fail("step seven: the page on seven's origin was not refused with the target text, or was listed with more than its origin: %+v", row.SevenBefore)
	case row.SevenAfter == nil:
		fail("step seven-released: after the page left seven's origin for an ordinary page, the client neither listed it at the new URL nor attached within the bound")
	case row.SevenAfter.Evaluated || !strings.Contains(row.SevenAfter.EvaluateError, "the target's history may restore a protected document"):
		fail("step seven-history: Runtime.evaluate on the released page whose history holds seven's page was not refused with the history text: %+v", *row.SevenAfter)
	case !row.Control:
		fail("step control: the client could not evaluate on an ordinary page before the mark")
	case row.Rewritten < 1 || !strings.Contains(lines, "default context marks it protected"):
		fail("step mark: the guard's own session did not mark the page from its default context (rewritten=%d)", row.Rewritten)
	case !row.ClientCut:
		fail("step cut: the nav-session mark did not detach the client session that never enabled Runtime")
	case !navContextOriginRefused(row.Marked):
		fail("step marked: the page marked from the guard's own session was not refused with the target text, or was listed with more than its origin: %+v", row.Marked)
	case row.After == nil:
		fail("step released: after the marked page navigated to an ordinary page (order %s), the client neither listed it at the new URL nor attached within the bound", row.ReleaseOrder.Order)
	case !row.After.Evaluated || row.After.Href != opts["second"]:
		fail("step released-evaluate: the released page did not evaluate to its new URL: %+v", *row.After)
	case !strings.Contains(lines, "cleared a target's default-context mark"):
		fail("step clear-log: the guard admitted the page without clearing its mark")
	}
}
