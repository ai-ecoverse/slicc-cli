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
	zwj    string
	xna    string
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
	env.zwj = "http://\u200d.sliccy.ai:" + pu.Port() + "/?n=zwj"
	env.xna = "http://xn--a.sliccy.ai:" + pu.Port() + "/?n=xna"
	env.unguardedCall(t, "Storage.setCookies", map[string]any{"cookies": []map[string]any{{"name": "seven", "value": sliccyCookie, "domain": ".sliccy.ai", "path": "/"}}}, nil)
	t.Run("unparseable destinations are refused", env.testDestinations)
	t.Run("Chrome loads none of the URLs it rejects", env.testControl)
	t.Run("a tab Chrome loaded on an unparseable sliccy.ai host is cut", func(t *testing.T) { env.testLoadedTab(t, env.hole, "exa%20mple", true) })
	t.Run("an exotic host that isn't protected is cut too", func(t *testing.T) { env.testLoadedTab(t, env.exotic, "an%20exotic", false) })
	t.Run("window.open to an unparseable sliccy.ai host is cut", func(t *testing.T) { env.testPopupTo(t, env.hole, "exa%20mple") })
	t.Run("window.open to an exotic host is cut too", func(t *testing.T) { env.testPopupTo(t, env.exotic, "an%20exotic") })
	t.Run("a tab that leaves for an unparseable host drops out of the page's target events", env.testLeavesForHole)
	t.Run("an error page for an unparseable sliccy.ai host still cuts the tab", env.testErrorPageForHole)
	t.Run("a blob from an opaque origin is protected", env.testOpaqueBlob)
	t.Run("a joiner host Chrome loads on sliccy.ai is cut", func(t *testing.T) { env.testLoadedForm(t, env.zwj, "xn--1ug.sliccy.ai", true, true) })
	t.Run("an invalid xn-- label Chrome loads on sliccy.ai is cut", func(t *testing.T) { env.testLoadedForm(t, env.xna, "xn--a.sliccy.ai", true, true) })
	t.Run("every other destination that doesn't parse is refused", env.testOtherDestinations)
	t.Run("the link still works", env.testStillLinked)
}

func (e *unparsedEnv) testDestinations(t *testing.T) {
	urls := append(append([]string{}, chromeRejects...), e.hole, e.exotic, e.zwj, e.xna)
	var r struct {
		Navigate map[string]struct {
			cdpOutcome
			Href string `json:"href"`
		} `json:"navigate"`
		Create map[string]cdpOutcome `json:"create"`
	}
	e.evalJSON(t, "linkTest.protect.privileged("+jsArgs("chrome", urls)+")", &r)
	for _, u := range urls {
		want := refusedInheritedText
		if strings.HasPrefix(u, "blob:") {
			want = refusedParseText
		}
		if nav := r.Navigate[u]; !nav.refused() || nav.Error.Message != want || nav.Href != "about:blank" {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	if href := e.loadedHref(t, targetID, marker); !strings.HasPrefix(href, "http://") || !strings.Contains(href, marker) || strings.Contains(href, sliccyCookie) != cookie {
		t.Fatalf("unguarded, the tab holds %q, want a real document on %s (sliccy.ai cookie %v)", href, marker, cookie)
	}
}

func (e *unparsedEnv) loadedHref(t *testing.T, targetID, marker string) string {
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
	_ = e.unguarded("", "Target.detachFromTarget", map[string]any{"sessionId": att.SessionID}, nil)
	return href
}

func (e *unparsedEnv) testLoadedTab(t *testing.T, u, marker string, cookie bool) {
	e.testLoadedForm(t, u, marker, cookie, false)
}

func (e *unparsedEnv) testLoadedForm(t *testing.T, u, marker string, cookie, optional bool) {
	var made struct {
		TargetID string `json:"targetId"`
	}
	e.unguardedCall(t, "Target.createTarget", map[string]any{"url": u}, &made)
	defer e.b.closeTarget(made.TargetID)
	if optional && !strings.Contains(e.loadedHref(t, made.TargetID, marker), marker) {
		t.Skipf("this Chrome doesn't load %q as %s, so there's no page to cut", u, marker)
	}
	e.realDocument(t, made.TargetID, marker, cookie)
	for _, tg := range e.targets(t) {
		if tg.TargetID == made.TargetID && (!optional || strings.Contains(tg.URL, marker) || strings.Contains(tg.Title, "cdp nav")) {
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
		Opener string                `json:"opener"`
		Popups []protectTarget       `json:"popups"`
		Attach map[string]cdpOutcome `json:"attach"`
		Cookie map[string]cdpOutcome `json:"cookies"`
		Leaked []string              `json:"leaked"`
	}
	e.evalJSON(t, "linkTest.protect.unparsedPopup("+jsArgs("chrome", e.allowed+"/doc?n=opener", u, []string{marker, sliccyCookie, "cdp nav"})+")", &r)
	defer e.b.closeTarget(r.Opener)
	if len(r.Popups) != 0 {
		t.Errorf("the page lists the popup: %+v", r.Popups)
	}
	var all struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			OpenerID string `json:"openerId"`
		} `json:"targetInfos"`
	}
	e.unguardedCall(t, "Target.getTargets", nil, &all)
	popup := ""
	for _, ti := range all.TargetInfos {
		if ti.OpenerID == r.Opener {
			popup = ti.TargetID
		}
	}
	if popup == "" {
		t.Fatalf("window.open made no popup of %s", r.Opener)
	}
	defer e.b.closeTarget(popup)
	e.realDocument(t, popup, marker, strings.Contains(u, "sliccy.ai"))
	var a map[string]json.RawMessage
	e.evalJSON(t, "linkTest.protect.attach("+jsArgs("chrome", popup)+")", &a)
	var o cdpOutcome
	_ = json.Unmarshal(a["Target.attachToTarget"], &o)
	if o.Error.Message != refusedText {
		t.Errorf("Target.attachToTarget on the popup: %s", a["Target.attachToTarget"])
	}
	if len(r.Cookie) != 0 || len(r.Leaked) != 0 || strings.Contains(string(a["info"]), marker) {
		t.Errorf("the page reached the popup: cookies %+v, saw %v, info %s", r.Cookie, r.Leaked, a["info"])
	}
}

func (e *unparsedEnv) testLeavesForHole(t *testing.T) {
	var ok struct {
		OK bool `json:"ok"`
	}
	e.evalJSON(t, "linkTest.protect.watchTargets('chrome')", &ok)
	if !ok.OK {
		t.Fatal("the page couldn't watch targets")
	}
	var made struct {
		TargetID string `json:"targetId"`
	}
	e.unguardedCall(t, "Target.createTarget", map[string]any{"url": e.allowed + "/doc?n=leaves"}, &made)
	defer e.b.closeTarget(made.TargetID)
	e.realDocument(t, made.TargetID, "n=leaves", false)
	var att struct {
		SessionID string `json:"sessionId"`
	}
	e.unguardedCall(t, "Target.attachToTarget", map[string]any{"targetId": made.TargetID, "flatten": true}, &att)
	time.Sleep(500 * time.Millisecond)
	var m struct {
		Mark int `json:"mark"`
	}
	e.evalJSON(t, "linkTest.protect.watchMark()", &m)
	if err := e.unguarded(att.SessionID, "Page.navigate", map[string]any{"url": e.hole}, nil); err != nil {
		t.Fatal(err)
	}
	e.realDocument(t, made.TargetID, "exa%20mple", true)
	time.Sleep(time.Second)
	var r struct {
		Before []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
		} `json:"before"`
		After []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
		} `json:"after"`
		Listed bool `json:"listed"`
	}
	e.evalJSON(t, "linkTest.protect.watchedTargets("+jsArgs(made.TargetID, m.Mark)+")", &r)
	if len(r.Before) == 0 {
		t.Fatal("the control: the page saw no event for the tab while it was ordinary")
	}
	if r.Listed {
		t.Errorf("Target.getTargets lists the tab after it left for %q", e.hole)
	}
	for _, ev := range r.After {
		if ev.URL != r.Before[len(r.Before)-1].URL {
			t.Errorf("after the tab left for %q the page got %s at %q", e.hole, ev.Method, ev.URL)
		}
	}
}

func (e *unparsedEnv) testErrorPageForHole(t *testing.T) {
	var r struct {
		TargetID string     `json:"targetId"`
		Detached bool       `json:"detached"`
		After    cdpOutcome `json:"after"`
		Close    cdpOutcome `json:"close"`
	}
	e.evalJSON(t, "linkTest.protect.leaveFor("+jsArgs("chrome", e.allowed+"/?n=hole-error", e.hole)+")", &r)
	defer e.b.closeTarget(r.TargetID)
	if href := (&navEnv{linkEnv: e.linkEnv}).directHref(t, r.TargetID); !strings.HasPrefix(href, "chrome-error://") {
		t.Fatalf("the tab is at %q, not on Chrome's error page for %q", href, e.hole)
	}
	if !r.Detached || !r.After.refused() || !r.Close.refused() {
		t.Fatalf("an error page for %q left the tab drivable: %+v", e.hole, r)
	}
}

func (e *unparsedEnv) testOpaqueBlob(t *testing.T) {
	var r struct {
		URL  string `json:"url"`
		Text string `json:"text"`
		Body struct {
			cdpOutcome
			Result struct {
				Body string `json:"body"`
			} `json:"result"`
		} `json:"body"`
		Navigate cdpOutcome `json:"navigate"`
		Href     struct {
			cdpOutcome
			Result string `json:"result"`
		} `json:"href"`
		Info string `json:"info"`
	}
	e.evalJSON(t, "linkTest.protect.opaqueBlob('chrome')", &r)
	if !strings.HasPrefix(r.URL, "blob:null/") || !strings.Contains(r.Text, "opaque blob") {
		t.Fatalf("the data: page made %q holding %q, want a blob:null/ URL", r.URL, r.Text)
	}
	if r.Body.OK || strings.Contains(r.Body.Result.Body, "opaque blob") {
		t.Errorf("Network.getResponseBody of the blob: %+v", r.Body)
	}
	if r.Navigate.Error.Message != refusedParseText || strings.Contains(r.Href.Result, "blob ok") || strings.Contains(r.Info, "blob:") {
		t.Errorf("the tab on the blob: navigate %+v, page %+v, listed at %q", r.Navigate, r.Href, r.Info)
	}
}

func (e *unparsedEnv) historyTab(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var made struct {
		TargetID string `json:"targetId"`
	}
	if err := e.b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": e.allowed + "/doc?n=history-start"}, &made); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.b.closeTarget(made.TargetID) })
	s, err := e.b.client.Attach(ctx, made.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	waitHref(t, s, e.allowed)
	for _, step := range []struct{ to, want string }{{e.hole, "http://exa%20mple.sliccy.ai"}, {e.allowed + "/doc?n=history-end", e.allowed + "/doc?n=history-end"}} {
		if err := s.Call(ctx, "Page.navigate", map[string]any{"url": step.to}, nil); err != nil {
			t.Fatal(err)
		}
		waitHref(t, s, step.want)
	}
	_ = s.Detach(ctx)
	return made.TargetID
}

func (e *unparsedEnv) testOtherDestinations(t *testing.T) {
	history := e.historyTab(t)
	var r map[string]json.RawMessage
	e.evalJSON(t, "linkTest.protect.parseRefusals("+jsArgs("chrome", e.allowed, e.hole, history, "exa%20mple")+")", &r)
	for _, k := range []string{"setCookie", "getCookies", "pageGetCookies", "deleteCookies", "setCookies", "storageSetCookies", "continueRequest", "loadNetworkResource", "pwaInstall", "pwaLaunch", "history"} {
		var o cdpOutcome
		if err := json.Unmarshal(r[k], &o); err != nil || o.Error.Message != refusedParseText {
			t.Errorf("%s with %q: %s", k, e.hole, r[k])
		}
	}
	var after struct {
		Own       cdpOutcome `json:"getCookiesOwn"`
		Continued cdpOutcome `json:"continued"`
		Body      string     `json:"body"`
		Legacy    cdpOutcome `json:"legacyEnable"`
		Intercept cdpOutcome `json:"continueIntercepted"`
		LegacyOK  cdpOutcome `json:"legacyContinued"`
	}
	b, _ := json.Marshal(r)
	_ = json.Unmarshal(b, &after)
	if !after.Own.OK {
		t.Errorf("Network.getCookies by the page's own URL: %+v", after.Own)
	}
	if !after.Continued.OK || strings.Contains(after.Body, "cdp nav hole") {
		t.Errorf("the paused request after the refusal: %+v, body %q", after.Continued, after.Body)
	}
	switch {
	case !after.Legacy.OK:
		t.Logf("this Chrome has no Network.setRequestInterception (%+v), so continueInterceptedRequest isn't tried", after.Legacy)
	case after.Intercept.Error.Message != refusedParseText || !after.LegacyOK.OK:
		t.Errorf("Network.continueInterceptedRequest with %q: %+v, then %+v", e.hole, after.Intercept, after.LegacyOK)
	}
}
