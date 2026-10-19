package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

const refusedParseText = "slicc: refused: slicc can't parse this URL, so it could be a protected page"

const sliccyCookie = "fake-value-0001"

var chromeRejects = []string{
	"http://[::1",
	"http://",
	"http://a:99999/",
	"http://1.2.3.4.5/",
	"http://%zz/",
	"http://[1::2::3]/",
	"http://a^b/",
	"blob:http://[::1",
}

type unparsedEnv struct {
	*protectEnv
	hole   string
	exotic string
}

func TestCDPFailsClosedOnUnparseableURLs(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.sliccy.ai 127.0.0.1, MAP *.cut.test 127.0.0.1")
	env := &unparsedEnv{protectEnv: setupProtectOn(t, b)}
	pu, err := url.Parse(env.plain.URL)
	if err != nil {
		t.Fatal(err)
	}
	env.hole = "http://exa mple.sliccy.ai:" + pu.Port() + "/?n=hole"
	env.exotic = "http://an exotic.cut.test:" + pu.Port() + "/?n=exotic"
	env.unguardedCall(t, "Storage.setCookies", map[string]any{"cookies": []map[string]any{{"name": "seven", "value": sliccyCookie, "domain": ".sliccy.ai", "path": "/"}}}, nil)
	t.Run("unparseable destinations are refused", env.testDestinations)
	t.Run("Chrome loads none of the URLs it rejects", env.testControl)
	t.Run("a tab Chrome loaded on an unparseable sliccy.ai host is cut", func(t *testing.T) { env.testLoadedTab(t, env.hole, "exa%20mple", true) })
	t.Run("an exotic host that isn't protected is cut too", func(t *testing.T) { env.testLoadedTab(t, env.exotic, "an%20exotic", false) })
	t.Run("window.open to an unparseable sliccy.ai host is cut", func(t *testing.T) { env.testPopupTo(t, env.hole, "exa%20mple") })
	t.Run("window.open to an exotic host is cut too", func(t *testing.T) { env.testPopupTo(t, env.exotic, "an%20exotic") })
	t.Run("the link still works", env.testStillLinked)
}

func (e *unparsedEnv) testDestinations(t *testing.T) {
	urls := append(append([]string{}, chromeRejects...), e.hole, e.exotic)
	var r struct {
		Navigate map[string]struct {
			cdpOutcome
			Href string `json:"href"`
		} `json:"navigate"`
		Create map[string]cdpOutcome `json:"create"`
	}
	e.evalJSON(t, "linkTest.protect.privileged("+jsArgs("chrome", urls)+")", &r)
	for _, u := range urls {
		if nav := r.Navigate[u]; nav.Error.Message != refusedParseText || nav.Href != "about:blank" {
			t.Errorf("Page.navigate %q: %+v", u, nav)
		}
		if c := r.Create[u]; c.Error.Message != refusedParseText {
			t.Errorf("Target.createTarget %q: %+v", u, c)
		}
	}
	var blank cdpOutcome
	e.evalJSON(t, "linkTest.protect.create("+jsArgs("chrome", "")+")", &blank)
	if !blank.OK {
		t.Errorf("Target.createTarget with an empty URL: %+v", blank)
	}
}

func (e *unparsedEnv) unguarded(sessionID, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return e.b.client.Call(ctx, sessionID, method, params, result)
}

func (e *unparsedEnv) unguardedHref(sessionID string) string {
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := e.unguarded(sessionID, "Runtime.evaluate", map[string]any{"expression": "location.href + ' ' + document.cookie", "returnByValue": true}, &r); err != nil {
		return ""
	}
	return r.Result.Value
}

func notADocument(href string) bool {
	return href == "" || strings.HasPrefix(href, "about:blank") || strings.HasPrefix(href, "chrome-error://")
}

func (e *unparsedEnv) testControl(t *testing.T) {
	for _, u := range chromeRejects {
		var tab, made struct {
			TargetID string `json:"targetId"`
		}
		var att struct {
			SessionID string `json:"sessionId"`
		}
		if err := e.unguarded("", "Target.createTarget", map[string]any{"url": "about:blank"}, &tab); err != nil {
			t.Fatal(err)
		}
		if err := e.unguarded("", "Target.attachToTarget", map[string]any{"targetId": tab.TargetID, "flatten": true}, &att); err != nil {
			t.Fatal(err)
		}
		var nav struct {
			ErrorText string `json:"errorText"`
		}
		err := e.unguarded(att.SessionID, "Page.navigate", map[string]any{"url": u}, &nav)
		time.Sleep(500 * time.Millisecond)
		if href := e.unguardedHref(att.SessionID); (err == nil && nav.ErrorText == "") || !notADocument(href) {
			t.Errorf("unguarded Page.navigate %q gave %v %+v and a page at %q", u, err, nav, href)
		}
		e.b.closeTarget(tab.TargetID)
		if err := e.unguarded("", "Target.createTarget", map[string]any{"url": u}, &made); err != nil {
			continue
		}
		time.Sleep(time.Second)
		var info struct {
			TargetInfo struct {
				URL string `json:"url"`
			} `json:"targetInfo"`
		}
		_ = e.unguarded("", "Target.getTargetInfo", map[string]any{"targetId": made.TargetID}, &info)
		href := ""
		if e.unguarded("", "Target.attachToTarget", map[string]any{"targetId": made.TargetID, "flatten": true}, &att) == nil {
			href = e.unguardedHref(att.SessionID)
		}
		if !notADocument(info.TargetInfo.URL) || !notADocument(href) {
			t.Errorf("unguarded Target.createTarget %q made a tab at %q with a page at %q", u, info.TargetInfo.URL, href)
		}
		e.b.closeTarget(made.TargetID)
	}
}

func (e *unparsedEnv) realDocument(t *testing.T, targetID, marker string, cookie bool) {
	t.Helper()
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := e.unguarded("", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &att); err != nil {
		t.Fatal(err)
	}
	var href string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if href = e.unguardedHref(att.SessionID); strings.Contains(href, marker) {
			break
		}
	}
	if !strings.HasPrefix(href, "http://") || !strings.Contains(href, marker) || strings.Contains(href, sliccyCookie) != cookie {
		t.Fatalf("unguarded, the tab holds %q, want a real document on %s (sliccy.ai cookie %v)", href, marker, cookie)
	}
	_ = e.unguarded("", "Target.detachFromTarget", map[string]any{"sessionId": att.SessionID}, nil)
}

func (e *unparsedEnv) testLoadedTab(t *testing.T, u, marker string, cookie bool) {
	var made struct {
		TargetID string `json:"targetId"`
	}
	e.unguardedCall(t, "Target.createTarget", map[string]any{"url": u}, &made)
	defer e.b.closeTarget(made.TargetID)
	e.realDocument(t, made.TargetID, marker, cookie)
	for _, tg := range e.targets(t) {
		if tg.TargetID == made.TargetID && (strings.Contains(tg.URL, marker) || strings.Contains(tg.Title, "cdp nav")) {
			t.Errorf("the tab is listed as %+v", tg)
		}
	}
	var r map[string]json.RawMessage
	e.evalJSON(t, "linkTest.protect.attach("+jsArgs("chrome", made.TargetID)+")", &r)
	for _, m := range []string{"Target.attachToTarget", "Target.activateTarget", "Target.closeTarget", "Browser.getWindowForTarget"} {
		var o cdpOutcome
		_ = json.Unmarshal(r[m], &o)
		if o.Error.Message != refusedText {
			t.Errorf("%s on the tab: %s", m, r[m])
		}
	}
	if strings.Contains(string(r["info"]), marker) {
		t.Errorf("Target.getTargetInfo shows the tab as %s", r["info"])
	}
}

func (e *unparsedEnv) testPopupTo(t *testing.T, u, marker string) {
	var r struct {
		Popups []protectTarget       `json:"popups"`
		Attach map[string]cdpOutcome `json:"attach"`
		Cookie map[string]cdpOutcome `json:"cookies"`
		Leaked []string              `json:"leaked"`
	}
	e.evalJSON(t, "linkTest.protect.unparsedPopup("+jsArgs("chrome", e.allowed+"/doc?n=opener", u, []string{marker, sliccyCookie, "cdp nav"})+")", &r)
	if len(r.Popups) != 1 {
		t.Fatalf("want one popup listed, got %+v", r.Popups)
	}
	p := r.Popups[0]
	defer e.b.closeTarget(p.TargetID)
	e.realDocument(t, p.TargetID, marker, strings.Contains(u, "sliccy.ai"))
	if a := r.Attach[p.TargetID]; a.Error.Message != refusedText {
		t.Errorf("Target.attachToTarget on the popup: %+v", a)
	}
	if len(r.Cookie) != 0 || len(r.Leaked) != 0 {
		t.Errorf("the page reached the popup: cookies %+v, saw %v", r.Cookie, r.Leaked)
	}
}
