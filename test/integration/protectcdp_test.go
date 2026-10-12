package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const refusedText = "slicc: refused: target is on seven's origin"

type protectEnv struct {
	*linkEnv
	plain   *httptest.Server
	allowed string
	seven   string
}

type cdpOutcome struct {
	OK    bool `json:"ok"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (o cdpOutcome) refused() bool {
	return !o.OK && o.Error.Code == -32000 && strings.HasPrefix(o.Error.Message, "slicc: refused: ")
}

type protectTarget struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	OpenerID string `json:"openerId"`
}

func setupProtect(t *testing.T) *protectEnv {
	t.Helper()
	b := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	env := &protectEnv{
		linkEnv: &linkEnv{t: t, b: b, origin: srv.URL},
		plain:   plain,
		allowed: strings.Replace(plain.URL, "127.0.0.1", "localhost", 1),
		seven:   srv.URL,
	}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--origin", plain.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	return env
}

func TestCDPProtectsSevensOrigin(t *testing.T) {
	env := setupProtect(t)
	t.Run("createTarget is refused", env.testCreate)
	t.Run("navigate is refused", env.testNavigate)
	t.Run("the seven tab is redacted and can't be attached", env.testAttach)
	t.Run("auto-attach skips the seven tab", env.testAutoAttach)
	t.Run("script navigation detaches", env.testScriptNavigate)
	t.Run("a blank popup from seven is seven's", env.testPopup)
	t.Run("history entries on seven are refused", env.testHistory)
	t.Run("cookies", env.testCookies)
	t.Run("websockets and event streams to seven", env.testSockets)
	t.Run("browser-wide commands", env.testBrowserWide)
	t.Run("own browser contexts", env.testOwnContext)
	t.Run("the relay forwards what it checked", env.testRawMessages)
	t.Run("the link still works", env.testStillLinked)
}

func (e *protectEnv) evalJSON(t *testing.T, expr string, out any) {
	t.Helper()
	var raw map[string]any
	e.p.eval(expr, &raw)
	b, _ := json.Marshal(raw)
	if _, failed := raw["error"].(string); failed || raw["reset"] != nil {
		t.Fatalf("%s: %s", expr, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: %v (%s)", expr, err, b)
	}
}

func (e *protectEnv) targets(t *testing.T) []protectTarget {
	t.Helper()
	var r struct {
		Targets []protectTarget `json:"targets"`
	}
	e.evalJSON(t, "linkTest.protect.targets('chrome')", &r)
	return r.Targets
}

func pageTargets(t *testing.T, b *chromeProc) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	infos, err := b.client.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, ti := range infos {
		if ti.Type == "page" {
			out[ti.TargetID] = ti.URL
		}
	}
	return out
}

func (e *protectEnv) testCreate(t *testing.T) {
	before := pageTargets(t, e.b)
	for _, u := range []string{e.seven + "/", e.seven + "/nowhere", "HTTP://127.0.0.1:" + e.seven[strings.LastIndexByte(e.seven, ':')+1:] + "/x", "blob:" + e.seven + "/0b9e", "https://seven.sliccy.ai/", "https://www.sliccy.ai/", "https://vault.sliccy.ai/"} {
		var r cdpOutcome
		e.evalJSON(t, "linkTest.protect.create("+jsArgs("chrome", u)+")", &r)
		if !r.refused() || r.Error.Message != refusedText {
			t.Fatalf("createTarget %s: %+v", u, r)
		}
	}
	for id, u := range pageTargets(t, e.b) {
		if _, ok := before[id]; !ok {
			t.Fatalf("a refused createTarget made a page at %s", u)
		}
	}
	var ok cdpOutcome
	e.evalJSON(t, "linkTest.protect.create("+jsArgs("chrome", e.allowed+"/?n=create")+")", &ok)
	if !ok.OK {
		t.Fatalf("createTarget on an allowed origin: %+v", ok)
	}
}

func (e *protectEnv) testNavigate(t *testing.T) {
	var r struct {
		cdpOutcome
		Href string `json:"href"`
	}
	e.evalJSON(t, "linkTest.protect.navigate("+jsArgs("chrome", e.seven+"/nowhere")+")", &r)
	if !r.refused() || r.Error.Message != refusedText || r.Href != "about:blank" {
		t.Fatalf("navigate to seven: %+v", r)
	}
	e.evalJSON(t, "linkTest.protect.navigate("+jsArgs("chrome", "view-source:"+e.seven+"/")+")", &r)
	if !r.refused() {
		t.Fatalf("navigate to view-source of seven: %+v", r)
	}
}

func (e *protectEnv) sevenTab(t *testing.T) protectTarget {
	t.Helper()
	for _, tg := range e.targets(t) {
		if tg.TargetID == e.p.target {
			return tg
		}
	}
	t.Fatal("Target.getTargets over the link doesn't list the seven tab")
	return protectTarget{}
}

func (e *protectEnv) testAttach(t *testing.T) {
	tab := e.sevenTab(t)
	if tab.URL != e.seven || tab.Title != e.seven {
		t.Fatalf("the seven tab is listed as %+v, want its URL and title redacted to %s", tab, e.seven)
	}
	var r map[string]json.RawMessage
	e.evalJSON(t, "linkTest.protect.attach("+jsArgs("chrome", tab.TargetID)+")", &r)
	for _, m := range []string{"Target.attachToTarget", "Target.activateTarget", "Target.closeTarget", "Target.exposeDevToolsProtocol", "Target.sendMessageToTarget", "Browser.getWindowForTarget"} {
		var o cdpOutcome
		_ = json.Unmarshal(r[m], &o)
		if !o.refused() {
			t.Fatalf("%s on the seven tab: %s", m, r[m])
		}
	}
	var info struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal(r["info"], &info)
	if info.URL != e.seven || info.Title != e.seven {
		t.Fatalf("Target.getTargetInfo: %+v", info)
	}
	var up bool
	e.p.eval("linkTest.status().up", &up)
	if !up {
		t.Fatal("the link went down")
	}
}

func (e *protectEnv) testAutoAttach(t *testing.T) {
	var r struct {
		Attached   []protectTarget `json:"attached"`
		Discovered []protectTarget `json:"discovered"`
		Plain      string          `json:"plain"`
	}
	e.evalJSON(t, "linkTest.protect.autoAttach("+jsArgs("chrome", e.allowed+"/?n=auto")+")", &r)
	plain := false
	for _, a := range r.Attached {
		if a.TargetID == e.p.target || strings.HasPrefix(a.URL, e.seven) {
			t.Fatalf("auto-attach yielded a session to seven: %+v", a)
		}
		plain = plain || a.TargetID == r.Plain
	}
	if !plain {
		t.Fatalf("auto-attach didn't attach the allowed tab: %+v", r.Attached)
	}
	for _, d := range r.Discovered {
		if d.TargetID == e.p.target && (d.URL != e.seven || d.Title != e.seven) {
			t.Fatalf("targetCreated for the seven tab: %+v", d)
		}
	}
}

func (e *protectEnv) testScriptNavigate(t *testing.T) {
	var r struct {
		TargetID string     `json:"targetId"`
		Before   string     `json:"before"`
		After    cdpOutcome `json:"after"`
		Reattach cdpOutcome `json:"reattach"`
		URL      string     `json:"url"`
		Title    string     `json:"title"`
		Leaked   int        `json:"leaked"`
	}
	e.evalJSON(t, "linkTest.protect.scriptNavigate("+jsArgs("chrome", e.allowed+"/?n=script", e.seven+"/nowhere?secret=1")+")", &r)
	if !strings.HasPrefix(r.Before, e.allowed) || !r.After.refused() || !r.Reattach.refused() || r.URL != e.seven || r.Title != e.seven || r.Leaked != 0 {
		t.Fatalf("script navigation onto seven: %+v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = e.b.client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": r.TargetID}, nil)
}

func (e *protectEnv) testPopup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var opened struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	params := map[string]any{"expression": "Boolean(window.open('about:blank', '_blank'))", "returnByValue": true, "userGesture": true}
	if err := e.p.session.Call(ctx, "Runtime.evaluate", params, &opened); err != nil || !opened.Result.Value {
		t.Fatalf("window.open in the seven tab: %+v %v", opened, err)
	}
	var r struct {
		URL    string     `json:"url"`
		Attach cdpOutcome `json:"attach"`
	}
	e.evalJSON(t, "linkTest.protect.popup("+jsArgs("chrome", e.p.target)+")", &r)
	if r.URL != "about:blank" || !r.Attach.refused() {
		t.Fatalf("the blank popup seven opened: %+v", r)
	}
}

func (e *protectEnv) testHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := e.b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": e.seven + "/nowhere?n=history"}, &res); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = e.b.client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": res.TargetID}, nil)
	}()
	s, err := e.b.client.Attach(ctx, res.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	waitHref(t, s, e.seven)
	if err := s.Call(ctx, "Page.navigate", map[string]any{"url": e.allowed + "/?n=history"}, nil); err != nil {
		t.Fatal(err)
	}
	waitHref(t, s, e.allowed)
	_ = s.Detach(ctx)
	var r struct {
		Refused cdpOutcome `json:"refused"`
		Allowed cdpOutcome `json:"allowed"`
		Entries int        `json:"entries"`
	}
	e.evalJSON(t, "linkTest.protect.history("+jsArgs("chrome", res.TargetID, e.seven)+")", &r)
	if !r.Refused.refused() || r.Refused.Error.Message != refusedText || !r.Allowed.OK || r.Entries < 2 {
		t.Fatalf("navigateToHistoryEntry: %+v", r)
	}
}

func waitHref(t *testing.T, s *cdp.Session, prefix string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var href string
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.Evaluate(ctx, "document.readyState === 'complete' ? location.href : ''", cdp.EvaluateOptions{ReturnByValue: true}, &href)
		cancel()
		if err == nil && strings.HasPrefix(href, prefix) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tab never loaded %s (at %q, %v)", prefix, href, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type cookieNames struct {
	Names []string `json:"names"`
	Error any      `json:"error"`
}

func (e *protectEnv) testCookies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seven := []map[string]any{
		{"name": "seven", "value": "secret", "domain": "127.0.0.1", "path": "/"},
		{"name": "chips", "value": "secret", "domain": "localhost", "path": "/", "secure": true, "partitionKey": map[string]any{"topLevelSite": "https://sliccy.ai", "hasCrossSiteAncestor": false}},
	}
	if err := e.b.client.Call(ctx, "", "Storage.setCookies", map[string]any{"cookies": seven}, nil); err != nil {
		t.Fatal(err)
	}
	var r struct {
		SetAllowed     cdpOutcome  `json:"setAllowed"`
		SetSevenURL    cdpOutcome  `json:"setSevenURL"`
		SetSevenDomain cdpOutcome  `json:"setSevenDomain"`
		SetOtherPort   cdpOutcome  `json:"setOtherPort"`
		SetCookies     cdpOutcome  `json:"setCookies"`
		SetPartitioned cdpOutcome  `json:"setPartitioned"`
		StorageSet     cdpOutcome  `json:"storageSet"`
		DeleteSeven    cdpOutcome  `json:"deleteSeven"`
		ClearBrowser   cdpOutcome  `json:"clearBrowser"`
		StorageClear   cdpOutcome  `json:"storageClear"`
		StorageGet     cookieNames `json:"storageGet"`
		All            cookieNames `json:"all"`
		ByURL          cookieNames `json:"byURL"`
	}
	e.evalJSON(t, "linkTest.protect.cookies("+jsArgs("chrome", e.allowed+"/", e.seven+"/", "127.0.0.1")+")", &r)
	if !r.SetAllowed.OK {
		t.Fatalf("setting a cookie on an allowed origin: %+v", r.SetAllowed)
	}
	for name, o := range map[string]cdpOutcome{"setCookie url": r.SetSevenURL, "setCookie domain": r.SetSevenDomain, "setCookie other port": r.SetOtherPort, "setCookies": r.SetCookies, "partitioned under seven": r.SetPartitioned, "Storage.setCookies": r.StorageSet, "deleteCookies": r.DeleteSeven, "clearBrowserCookies": r.ClearBrowser, "Storage.clearCookies": r.StorageClear} {
		if !o.refused() {
			t.Errorf("%s: %+v", name, o)
		}
	}
	for name, n := range map[string]cookieNames{"Storage.getCookies": r.StorageGet, "Network.getAllCookies": r.All, "Network.getCookies": r.ByURL} {
		joined := strings.Join(n.Names, " ")
		if n.Error != nil || !strings.Contains(joined, "allowed@localhost") || strings.Contains(joined, "127.0.0.1") || strings.Contains(joined, "chips") {
			t.Errorf("%s: %+v", name, n)
		}
	}
	var direct struct {
		Cookies []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"cookies"`
	}
	if err := e.b.client.Call(ctx, "", "Storage.getCookies", map[string]any{}, &direct); err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, c := range direct.Cookies {
		if c.Domain == "127.0.0.1" && c.Name != "seven" {
			t.Errorf("a cookie for seven's host got through: %+v", c)
		}
		kept[c.Name] = true
	}
	if !kept["seven"] || !kept["chips"] {
		t.Errorf("seven's own cookies are gone: %v", kept)
	}
}

func (e *protectEnv) testBrowserWide(t *testing.T) {
	var r map[string]cdpOutcome
	e.evalJSON(t, "linkTest.protect.browserWide("+jsArgs("chrome", e.seven, e.allowed)+")", &r)
	for _, k := range []string{"permSeven", "grantAll", "grantSeven", "resetPerms", "clearData", "clearKey", "quota", "caches", "cacheEntries", "idb", "fsDir", "swUnregister", "expose", "tracing", "fetchRoot", "loadResource"} {
		if !r[k].refused() {
			t.Errorf("%s: %+v", k, r[k])
		}
	}
	for _, k := range []string{"permAllowed", "fetchPage"} {
		if !r[k].OK {
			t.Errorf("%s: %+v", k, r[k])
		}
	}
}

func (e *protectEnv) testOwnContext(t *testing.T) {
	var r map[string]cdpOutcome
	e.evalJSON(t, "linkTest.protect.ownContext('chrome')", &r)
	for _, k := range []string{"set", "clear", "resetPerms"} {
		if !r[k].OK {
			t.Errorf("%s in a browser context this stream made: %+v", k, r[k])
		}
	}
}

func (e *protectEnv) testStillLinked(t *testing.T) {
	var up bool
	e.p.eval("linkTest.status().up", &up)
	if !up {
		t.Fatal("the link went down")
	}
	var r struct {
		Title  string    `json:"title"`
		Href   string    `json:"href"`
		Closed cdpClosed `json:"closed"`
	}
	url := e.allowed + "/page?n=still"
	e.p.eval("linkTest.cdp.navigate("+jsArgs("chrome", url)+")", &r)
	if r.Title != "cdp nav still" || r.Href != url || !r.Closed.End {
		t.Fatalf("navigate to an allowed origin: %+v", r)
	}
}

func (e *protectEnv) testRawMessages(t *testing.T) {
	seven := e.seven + "/nowhere"
	msgs := [][2]any{
		{9001, `{"id":9001,"method":"Target.createTarget","params":{"url":"about:blank","url":"` + seven + `"}}`},
		{9002, `{"id":9002,"method":"Target.createTarget","params":{"url":"` + seven + `","url":"about:blank"}}`},
		{9003, `{"id":9003,"method":"Target.exposeDevToolsProtocol","params":{},"method":"Browser.getVersion"}`},
		{9004, `{"id":9004,"method":"Target.createTarget","params":{"url":"` + strings.Replace(seven, "/nowhere", `\/nowhere`, 1) + `"}}`},
		{9006, `{"id":9006,"method":"Target.createTarget","params":{"url":"` + e.allowed + `/?n=raw","URL":"` + seven + `"}}`},
	}
	var r struct {
		Out []struct {
			cdpOutcome
			URL    string         `json:"url"`
			Result map[string]any `json:"result"`
		} `json:"out"`
	}
	e.evalJSON(t, "linkTest.protect.raw("+jsArgs("chrome", msgs[:4])+")", &r)
	if len(r.Out) != 4 {
		t.Fatalf("raw: %+v", r)
	}
	if !r.Out[0].refused() || !r.Out[3].refused() {
		t.Fatalf("a duplicate or escaped url got through: %+v", r.Out)
	}
	if !r.Out[1].OK || r.Out[1].URL != "about:blank" {
		t.Fatalf("the browser saw another url than the relay: %+v", r.Out[1])
	}
	if !r.Out[2].OK || r.Out[2].Result["product"] == nil {
		t.Fatalf("the browser saw another method than the relay: %+v", r.Out[2])
	}
	e.evalJSON(t, "linkTest.protect.raw("+jsArgs("chrome", msgs[4:])+")", &r)
	if len(r.Out) != 1 || !r.Out[0].OK || !strings.HasPrefix(r.Out[0].URL, e.allowed) {
		t.Fatalf("a differently cased key: %+v", r.Out)
	}
}

func (e *protectEnv) testSockets(t *testing.T) {
	sevenBase := e.plain.URL
	var r struct {
		Opened [][]bool                  `json:"opened"`
		Counts map[string]map[string]int `json:"counts"`
		Leaked string                    `json:"leaked"`
	}
	e.evalJSON(t, "linkTest.protect.sockets("+jsArgs("chrome", e.allowed+"/?n=sockets", []string{sevenBase, e.allowed})+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Opened) != 2 || !r.Opened[1][0] || !r.Opened[1][1] {
		t.Fatalf("the sockets didn't work: %s", b)
	}
	allowed, seven := r.Counts[e.allowed], r.Counts[sevenBase]
	for _, m := range []string{"Network.webSocketWillSendHandshakeRequest", "Network.webSocketHandshakeResponseReceived", "Network.eventSourceMessageReceived"} {
		if allowed[m] == 0 {
			t.Fatalf("no %s for the allowed origin, so the filter went untested: %s", m, b)
		}
		if seven[m] != 0 {
			t.Errorf("%s reached the page for seven: %s", m, b)
		}
	}
	if r.Leaked != "" {
		t.Errorf("%s carried seven's secret: %s", r.Leaked, b)
	}
}
