package cdp

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	liPage           = "LI-PAGE"
	liPageSession    = "S-LI-PAGE"
	liGuardSession   = "S-LI-GUARD"
	liSeven          = "LI-SEVEN"
	liSevenSession   = "S-LI-SEVEN"
	liBrowserSession = "S-LI-BROWSER"
	liIframe         = "LI-IFRAME"
	liIframeSession  = "S-LI-IFRAME"
	liWorker         = "LI-WORKER"
	liWorkerSession  = "S-LI-WORKER"
	liPageURL        = "https://page.ordinary.test/app"
	liDataURL        = "https://page.ordinary.test/data"
	liSevenURL       = "https://seven.sliccy.ai/doc"
	liSevenData      = "https://seven.sliccy.ai/data"
	liVaultData      = "https://vault.sliccy.ai/data"
	liHanded         = "interception-job-7.0"
	liGuessed        = "interception-job-99.0"
	liRefused        = "slicc: refused: the guard doesn’t support the legacy Network interception methods; intercept requests through Fetch instead"
	liTargetRefused  = "slicc: refused: target is on seven's origin"
	liPausedRefused  = "slicc: refused: slicc didn't pass this paused request to the page, so it could be seven's"
	liUnflatRefused  = "slicc: refused: Target.sendMessageToTarget can't be checked; attach with flatten: true"
	liLostRefused    = "slicc: refused: slicc lost track of the browser's targets"
	liHistoryRefused = "slicc: refused: the target's history may restore a protected document"
)

type liWire struct {
	t      *testing.T
	g      *guard
	mu     sync.Mutex
	chrome []string
	client []string
	async  int
}

func liNew(t *testing.T) *liWire {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	w := &liWire{t: t}
	w.g = newGuard(o, nil)
	w.g.relaySession = "RELAY"
	w.g.flushAsync = func(send, page [][]byte) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.async++
		for _, m := range send {
			w.chrome = append(w.chrome, string(m))
		}
		for _, m := range page {
			w.client = append(w.client, string(m))
		}
	}
	w.g.observeAll([]targetInfo{
		{TargetID: liPage, Type: "page", URL: liPageURL, BrowserContextID: "CTX"},
		{TargetID: liSeven, Type: "page", URL: liSevenURL, BrowserContextID: "CTX"},
		{TargetID: liIframe, Type: "iframe", URL: "https://frame.other.test/inner", ParentFrameID: liPage, BrowserContextID: "CTX"},
		{TargetID: liWorker, Type: "service_worker", URL: "https://page.ordinary.test/sw.js", BrowserContextID: "CTX"},
	})
	w.g.mu.Lock()
	w.g.sessions[liPageSession] = &session{id: liPageSession, target: liPage, typ: "page"}
	w.g.sessions[liSevenSession] = &session{id: liSevenSession, target: liSeven, typ: "page"}
	w.g.sessions[liIframeSession] = &session{id: liIframeSession, target: liIframe, parent: liPageSession, typ: "iframe"}
	w.g.sessions[liWorkerSession] = &session{id: liWorkerSession, target: liWorker, parent: liPageSession, typ: "service_worker"}
	nt := &navTarget{target: liPage, session: liGuardSession, ready: make(chan struct{}), done: true}
	w.g.navTargets[liPage] = nt
	w.g.navSessions[liGuardSession] = nt
	for target, guard := range map[string]string{liIframe: liGuardSession + "-IFRAME", liWorker: liGuardSession + "-WORKER"} {
		nt := &navTarget{target: target, session: guard, ready: make(chan struct{}), done: true, worker: target == liWorker}
		w.g.navTargets[target] = nt
		w.g.navSessions[guard] = nt
	}
	w.g.mu.Unlock()
	v := w.cmd(`{"id":1,"method":"Target.attachToBrowserTarget"}`)
	if string(v.forward) != `{"id":1,"method":"Target.attachToBrowserTarget"}` {
		t.Fatalf("attachToBrowserTarget was not forwarded: %+v", v)
	}
	w.in(`{"id":1,"result":{"sessionId":"` + liBrowserSession + `"}}`)
	w.reset()
	return w
}

func (w *liWire) reset() {
	w.mu.Lock()
	w.chrome, w.client, w.async = nil, nil, 0
	w.mu.Unlock()
}

func (w *liWire) cmd(raw string) pageVerdict {
	w.t.Helper()
	c, id, ok := parseCommand([]byte(raw))
	v := w.g.fromCommand(c, id, ok)
	if v.blocks() {
		w.t.Fatalf("the command waits instead of being decided: %s", raw)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if v.reply != nil {
		w.client = append(w.client, string(v.reply))
	}
	for _, m := range v.send {
		w.chrome = append(w.chrome, string(m))
	}
	if v.forward != nil {
		w.chrome = append(w.chrome, string(v.forward))
	}
	return v
}

func (w *liWire) in(raw string) browserVerdict {
	w.t.Helper()
	v := w.g.fromBrowser([]byte(raw))
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range v.send {
		w.chrome = append(w.chrome, string(m))
	}
	if v.forward != nil {
		w.client = append(w.client, string(v.forward))
	}
	for _, m := range v.page {
		w.client = append(w.client, string(m))
	}
	return v
}

func (w *liWire) wire() ([]string, []string, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.chrome...), append([]string(nil), w.client...), w.async
}

func liSession(session string) string {
	if session == "" {
		return ""
	}
	return `,"sessionId":"` + session + `"`
}

func liRefusal(id int64, session, message string) string {
	return `{"error":{"code":-32000,"message":"` + message + `"},"id":` + fmt.Sprint(id) + liSession(session) + `}`
}

func (w *liWire) seenByGuard(network, url string) {
	w.seenOn(liGuardSession, network, url)
}

func (w *liWire) seenOn(guard, network, url string) {
	w.in(`{"method":"Network.requestWillBeSent","sessionId":"` + guard + `","params":{"requestId":"` + network + `","loaderId":"L","documentURL":"` + liPageURL + `","request":{"url":"` + url + `","method":"GET","headers":{}},"type":"XHR","frameId":"` + liPage + `"}}`)
}

func liInterceptedEvent(session, id, network, url, typ, extra string) string {
	return `{"method":"Network.requestIntercepted","params":{"interceptionId":"` + id + `","request":{"url":"` + url + `","method":"GET","headers":{}},"frameId":"` + liPage + `","resourceType":"` + typ + `","isNavigationRequest":` + fmt.Sprint(typ == "Document") + `,"requestId":"` + network + `"` + extra + `}` + liSession(session) + `}`
}

func (w *liWire) intercepted(session, id, network, url, typ, extra string) browserVerdict {
	w.t.Helper()
	switch session {
	case liPageSession:
		w.seenByGuard(network, url)
	case liIframeSession:
		w.seenOn(liGuardSession+"-IFRAME", network, url)
	case liWorkerSession:
		w.seenOn(liGuardSession+"-WORKER", network, url)
	}
	return w.in(liInterceptedEvent(session, id, network, url, typ, extra))
}

func (w *liWire) handOut(session string) {
	w.t.Helper()
	v := w.intercepted(session, liHanded, "N7", liDataURL, "XHR", "")
	if v.forward == nil || !strings.Contains(string(v.forward), liHanded) || len(v.send) != 0 {
		w.t.Fatalf("the ordinary interception was not shown to the client: %+v", v)
	}
	w.g.mu.Lock()
	handed := w.g.handed[requestKey{session, liHanded}]
	w.g.mu.Unlock()
	if handed == 0 {
		w.t.Fatalf("the ordinary interception was not handed to session %q", session)
	}
}

type liForm struct {
	name, method, params string
}

func liForms(id string) []liForm {
	raw := base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	return []liForm{
		{"set-enable", "Network.setRequestInterception", `{"patterns":[{"urlPattern":"*"}]}`},
		{"set-stage", "Network.setRequestInterception", `{"patterns":[{"urlPattern":"*","resourceType":"Document","interceptionStage":"HeadersReceived"}]}`},
		{"set-disable", "Network.setRequestInterception", `{"patterns":[]}`},
		{"continue-id", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `"}`},
		{"continue-headers", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","headers":{"X-Li":"1"}}`},
		{"continue-method", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","method":"POST"}`},
		{"continue-postdata", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","postData":"a=1"}`},
		{"continue-error", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","errorReason":"Failed"}`},
		{"continue-auth", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","authChallengeResponse":{"response":"ProvideCredentials","username":"u","password":"p"}}`},
		{"continue-auth-default", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","authChallengeResponse":{"response":"Default"}}`},
		{"continue-url", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","url":"https://page.ordinary.test/other"}`},
		{"continue-raw", "Network.continueInterceptedRequest", `{"interceptionId":"` + id + `","rawResponse":"` + raw + `"}`},
		{"continue-empty", "Network.continueInterceptedRequest", `{}`},
		{"body", "Network.getResponseBodyForInterception", `{"interceptionId":"` + id + `"}`},
		{"stream", "Network.takeResponseBodyForInterceptionAsStream", `{"interceptionId":"` + id + `"}`},
	}
}

var liSessions = []struct{ name, id string }{
	{"root", ""},
	{"page", liPageSession},
	{"browser", liBrowserSession},
	{"iframe", liIframeSession},
	{"worker", liWorkerSession},
}

func liLate(forms []liForm) []liForm {
	var out []liForm
	for _, f := range forms {
		if f.name != "continue-url" && f.name != "continue-raw" {
			out = append(out, f)
		}
	}
	return out
}

func liEarly(forms []liForm) []liForm {
	var out []liForm
	for _, f := range forms {
		if f.name == "continue-url" || f.name == "continue-raw" {
			out = append(out, f)
		}
	}
	return out
}

type liState struct {
	pending, bodyCalls, continues, clientCalls, heldAnswers, relayCalls, handed, pauseLinks, clientRequests int
}

func (w *liWire) state() liState {
	w.g.mu.Lock()
	defer w.g.mu.Unlock()
	return liState{len(w.g.pending), len(w.g.bodyCalls), len(w.g.continues), len(w.g.clientCalls), len(w.g.heldAnswers), len(w.g.relayCalls), len(w.g.handed), len(w.g.pauseLinks), len(w.g.clientRequests)}
}

func (w *liWire) mustRefuse(id int64, session string, f liForm) {
	w.t.Helper()
	chromeBefore, clientBefore, asyncBefore := w.wire()
	before := w.state()
	raw := `{"id":` + fmt.Sprint(id) + `,"method":"` + f.method + `","params":` + f.params + liSession(session) + `}`
	v := w.cmd(raw)
	want := liRefusal(id, session, liRefused)
	switch {
	case v.forward != nil:
		w.t.Fatalf("%s: admitted and forwarded to Chrome: %s", f.name, v.forward)
	case string(v.reply) != want:
		w.t.Fatalf("%s: answered\n%s\nwant\n%s", f.name, v.reply, want)
	case len(v.send) != 0:
		w.t.Fatalf("%s: sent to Chrome alongside the refusal: %q", f.name, v.send)
	}
	chrome, client, async := w.wire()
	if len(chrome) != len(chromeBefore) || len(client) != len(clientBefore)+1 || async != asyncBefore {
		w.t.Fatalf("%s: extra messages: chrome %q client %q async %d", f.name, chrome[len(chromeBefore):], client[len(clientBefore):], async-asyncBefore)
	}
	if after := w.state(); after != before {
		w.t.Fatalf("%s: the refusal kept state: %+v, before %+v", f.name, after, before)
	}
}

func TestLegacyInterceptionClientRefused(t *testing.T) {
	for _, s := range liSessions {
		for _, state := range []string{"handed", "guessed"} {
			t.Run(s.name+"/"+state, func(t *testing.T) {
				id := liGuessed
				if state == "handed" {
					id = liHanded
				}
				for i, f := range liForms(id) {
					t.Run(f.name, func(t *testing.T) {
						w := liNew(t)
						if id == liHanded {
							w.handOut(s.id)
						}
						w.mustRefuse(int64(40+i), s.id, f)
					})
				}
			})
		}
	}
}

func liMissing(id int64, session string) string {
	return `{"error":{"code":-32001,"message":"Session with given id not found."},"id":` + fmt.Sprint(id) + liSession(session) + `}`
}

type liStateRow struct {
	name    string
	session string
	prepare func(w *liWire)
	want    func(id int64, session string) string
}

func liStateRows() []liStateRow {
	return []liStateRow{
		{"protected-session", liSevenSession, func(*liWire) {}, func(id int64, s string) string { return liRefusal(id, s, liTargetRefused) }},
		{"unknown-session", "S-LI-UNKNOWN", func(*liWire) {}, liMissing},
		{"gone-session", "S-LI-GONE", func(w *liWire) {
			w.g.mu.Lock()
			w.g.gone["S-LI-GONE"] = true
			w.g.mu.Unlock()
		}, liMissing},
		{"held-session", "S-LI-HELD", func(w *liWire) {
			w.g.mu.Lock()
			w.g.held["S-LI-HELD"] = &heldSession{id: "S-LI-HELD", typ: "page"}
			w.g.mu.Unlock()
		}, liMissing},
		{"lost-stream", liPageSession, func(w *liWire) {
			w.g.mu.Lock()
			w.g.lost = true
			w.g.mu.Unlock()
		}, func(id int64, s string) string { return liRefusal(id, s, liLostRefused) }},
	}
}

func (w *liWire) mustAnswer(id int64, session, method, params, want string) {
	w.t.Helper()
	chromeBefore, _, _ := w.wire()
	before := w.state()
	v := w.cmd(`{"id":` + fmt.Sprint(id) + `,"method":"` + method + `","params":` + params + liSession(session) + `}`)
	if v.forward != nil || len(v.send) != 0 || string(v.reply) != want {
		w.t.Fatalf("%s: forward=%s send=%q reply\n%s\nwant\n%s", method, v.forward, v.send, v.reply, want)
	}
	if chrome, _, _ := w.wire(); len(chrome) != len(chromeBefore) {
		w.t.Fatalf("%s: sent to Chrome: %q", method, chrome[len(chromeBefore):])
	}
	if after := w.state(); after != before {
		w.t.Fatalf("%s: kept state: %+v, before %+v", method, after, before)
	}
}

func TestLegacyInterceptionProtectedTwin(t *testing.T) {
	for _, row := range liStateRows() {
		for _, order := range []string{"legacy-first", "other-first"} {
			t.Run(row.name+"/"+order, func(t *testing.T) {
				w := liNew(t)
				row.prepare(w)
				others := []liForm{
					{"fetch-enable", "Fetch.enable", `{"patterns":[{"urlPattern":"*"}]}`},
					{"fetch-continue", "Fetch.continueRequest", `{"requestId":"` + liGuessed + `"}`},
					{"runtime-evaluate", "Runtime.evaluate", `{"expression":"1"}`},
				}
				legacy := liLate(liForms(liGuessed))
				run := func(forms []liForm, base int64) {
					for i, f := range forms {
						id := base + int64(i)
						w.mustAnswer(id, row.session, f.method, f.params, row.want(id, row.session))
					}
				}
				if order == "legacy-first" {
					run(legacy, 700)
					run(others, 750)
					run(legacy, 800)
				} else {
					run(others, 750)
					run(legacy, 700)
					run(others, 850)
				}
			})
		}
	}
}

func TestLegacyInterceptionBecomesProtected(t *testing.T) {
	for _, order := range []string{"refused-then-locked", "locked-then-refused"} {
		t.Run(order, func(t *testing.T) {
			w := liNew(t)
			lock := func() {
				v := w.in(`{"method":"Fetch.requestPaused","sessionId":"` + liGuardSession + `","params":{"requestId":"interception-job-8.0","request":{"url":"` + liSevenURL + `","method":"GET","headers":{}},"frameId":"` + liPage + `","resourceType":"Document","networkId":"N8"}}`)
				w.g.mu.Lock()
				locked := w.g.navTargets[liPage].historyLocked
				w.g.mu.Unlock()
				if !locked || !strings.Contains(strings.Join(liStrings(v.send), " "), `"Fetch.failRequest"`) {
					t.Fatalf("the guard did not lock the tab on its own paused protected document: %+v", v)
				}
			}
			if order == "refused-then-locked" {
				for i, f := range liForms(liGuessed) {
					w.mustRefuse(int64(900+i), liPageSession, f)
				}
			}
			lock()
			for round := 0; round < 2; round++ {
				for i, f := range liForms(liGuessed) {
					id := int64(950 + round*20 + i)
					if f.method == "Network.continueInterceptedRequest" {
						w.mustRefuse(id, liPageSession, f)
						continue
					}
					w.mustAnswer(id, liPageSession, f.method, f.params, liRefusal(id, liPageSession, liHistoryRefused))
				}
				w.mustAnswer(int64(990+round), liPageSession, "Runtime.evaluate", `{"expression":"1"}`, liRefusal(int64(990+round), liPageSession, liHistoryRefused))
			}
		})
	}
}

func TestLegacyInterceptionEarlyFormsEverywhere(t *testing.T) {
	for _, row := range liStateRows() {
		t.Run(row.name, func(t *testing.T) {
			w := liNew(t)
			row.prepare(w)
			for round := 0; round < 2; round++ {
				for i, f := range liEarly(liForms(liHanded)) {
					w.mustRefuse(int64(1000+round*10+i), row.session, f)
				}
			}
		})
	}
}

func TestLegacyInterceptionUnflattenedWrapperKeepsItsReason(t *testing.T) {
	for _, f := range liForms(liHanded) {
		t.Run(f.name, func(t *testing.T) {
			w := liNew(t)
			inner := strings.ReplaceAll(`{"id":5,"method":"`+f.method+`","params":`+f.params+`}`, `"`, `\"`)
			for _, s := range []string{"", liBrowserSession} {
				v := w.cmd(`{"id":81,"method":"Target.sendMessageToTarget","params":{"sessionId":"` + liPageSession + `","message":"` + inner + `"}` + liSession(s) + `}`)
				if v.forward != nil || string(v.reply) != liRefusal(81, s, liUnflatRefused) {
					t.Fatalf("Target.sendMessageToTarget carrying %s: forward=%s reply=%s", f.method, v.forward, v.reply)
				}
			}
			if chrome, _, _ := w.wire(); len(chrome) != 0 {
				t.Fatalf("sent to Chrome: %q", chrome)
			}
		})
	}
}

func TestLegacyInterceptionBodyOfGuardPause(t *testing.T) {
	for _, s := range liSessions[:3] {
		t.Run(s.name, func(t *testing.T) {
			w := liNew(t)
			v := w.in(`{"method":"Fetch.requestPaused","sessionId":"` + liGuardSession + `","params":{"requestId":"interception-job-9.0","request":{"url":"` + liSevenURL + `","method":"GET","headers":{}},"frameId":"LI-CHILD","resourceType":"Document","networkId":"N9"}}`)
			if v.forward != nil || len(v.send) == 0 || !strings.Contains(strings.Join(liStrings(v.send), " "), `"Fetch.failRequest"`) {
				t.Fatalf("the guard did not fail its own paused protected document: %+v", v)
			}
			for i, f := range liForms("interception-job-9.0") {
				if f.method == "Network.getResponseBodyForInterception" || f.method == "Network.takeResponseBodyForInterceptionAsStream" {
					w.mustRefuse(int64(90+i), s.id, f)
				}
			}
		})
	}
}

func liStrings(b [][]byte) []string {
	out := make([]string, 0, len(b))
	for _, m := range b {
		out = append(out, string(m))
	}
	return out
}

func TestLegacyInterceptionSameValue(t *testing.T) {
	for _, s := range liSessions {
		t.Run(s.name, func(t *testing.T) {
			w := liNew(t)
			w.handOut(s.id)
			before := w.state()
			for round := 0; round < 3; round++ {
				for i, f := range liForms(liHanded) {
					w.mustRefuse(int64(100+round*100+i), s.id, f)
				}
			}
			if after := w.state(); after != before {
				t.Fatalf("repeated refusals kept state: %+v, before %+v", after, before)
			}
			w.g.mu.Lock()
			handed := w.g.handed[requestKey{s.id, liHanded}]
			w.g.mu.Unlock()
			if handed == 0 {
				t.Fatal("the refusals dropped the handed interception")
			}
			v := w.cmd(`{"id":399,"method":"Fetch.continueRequest","params":{"requestId":"` + liGuessed + `"}` + liSession(s.id) + `}`)
			if string(v.reply) != liRefusal(399, s.id, liPausedRefused) {
				t.Fatalf("a Fetch command after the refusals: %s %s", v.forward, v.reply)
			}
		})
	}
}

func liPaused(id, network, url, extra string) string {
	return `{"method":"Fetch.requestPaused","sessionId":"` + liPageSession + `","params":{"requestId":"` + id + `","request":{"url":"` + url + `","method":"GET","headers":{}},"frameId":"` + liPage + `","resourceType":"XHR","networkId":"` + network + `"` + extra + `}}`
}

type liStep struct {
	in     bool
	raw    string
	reply  string
	before func(w *liWire)
}

func liStockSteps(enable string) []liStep {
	s := liSession(liPageSession)
	resp := `,"responseStatusCode":200,"responseHeaders":[{"name":"Content-Type","value":"text/plain"}]`
	seen := func(network, url string) func(w *liWire) {
		return func(w *liWire) { w.seenByGuard(network, url) }
	}
	return []liStep{
		{raw: `{"id":11,"method":"Network.enable","params":{}` + s + `}`, reply: `{"id":11,"result":{}` + s + `}`},
		{raw: `{"id":12,"method":"Fetch.enable","params":` + enable + s + `}`, reply: `{"id":12,"result":{}` + s + `}`},
		{in: true, before: seen("N1", liDataURL+"?1"), raw: liPaused("interception-job-1.0", "N1", liDataURL+"?1", "")},
		{raw: `{"id":13,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-1.0"}` + s + `}`, reply: `{"id":13,"result":{}` + s + `}`},
		{in: true, before: seen("N2", liDataURL+"?2"), raw: liPaused("interception-job-2.0", "N2", liDataURL+"?2", "")},
		{raw: `{"id":14,"method":"Fetch.fulfillRequest","params":{"requestId":"interception-job-2.0","responseCode":200,"responseHeaders":[{"name":"Content-Type","value":"text/plain"}],"body":"b2s="}` + s + `}`, reply: `{"id":14,"result":{}` + s + `}`},
		{in: true, before: seen("N3", liDataURL+"?3"), raw: liPaused("interception-job-3.0", "N3", liDataURL+"?3", "")},
		{raw: `{"id":15,"method":"Fetch.failRequest","params":{"requestId":"interception-job-3.0","errorReason":"Failed"}` + s + `}`, reply: `{"id":15,"result":{}` + s + `}`},
		{in: true, before: seen("N4", liDataURL+"?4"), raw: liPaused("interception-job-4.0", "N4", liDataURL+"?4", resp)},
		{raw: `{"id":16,"method":"Fetch.getResponseBody","params":{"requestId":"interception-job-4.0"}` + s + `}`, reply: `{"id":16,"result":{"body":"b2s=","base64Encoded":true}` + s + `}`},
		{raw: `{"id":17,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-4.0"}` + s + `}`, reply: `{"id":17,"result":{}` + s + `}`},
		{raw: `{"id":18,"method":"Fetch.disable","params":{}` + s + `}`, reply: `{"id":18,"result":{}` + s + `}`},
	}
}

func TestLegacyInterceptionStockFetchTranscriptUnchanged(t *testing.T) {
	for _, row := range []struct {
		name, enable string
		chrome       []string
		client       []string
	}{
		{"puppeteer", `{"handleAuthRequests":true,"patterns":[{"urlPattern":"*"}]}`, liStockChromePuppeteer, liStockClient},
		{"playwright", `{"handleAuthRequests":true,"patterns":[{"urlPattern":"*","requestStage":"Request"},{"urlPattern":"*","requestStage":"Response"}]}`, liStockChromePlaywright, liStockClient},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := liNew(t)
			for _, step := range liStockSteps(row.enable) {
				if step.before != nil {
					step.before(w)
				}
				if step.in {
					w.in(step.raw)
					continue
				}
				v := w.cmd(step.raw)
				if v.forward == nil {
					t.Fatalf("not forwarded: %s -> %s", step.raw, v.reply)
				}
				w.in(step.reply)
			}
			chrome, client, async := w.wire()
			if async != 0 {
				t.Fatalf("async messages: %d", async)
			}
			if strings.Join(chrome, "\n") != strings.Join(row.chrome, "\n") {
				t.Fatalf("chrome transcript changed:\n%s\nwant\n%s", strings.Join(chrome, "\n"), strings.Join(row.chrome, "\n"))
			}
			if strings.Join(client, "\n") != strings.Join(row.client, "\n") {
				t.Fatalf("client transcript changed:\n%s\nwant\n%s", strings.Join(client, "\n"), strings.Join(row.client, "\n"))
			}
		})
	}
}

func TestLegacyInterceptionGuardOwnFallbackUnchanged(t *testing.T) {
	auth := `,"authChallenge":{"source":"Server","origin":"https://seven.sliccy.ai","scheme":"basic","realm":"r"}`
	for _, s := range liSessions[:3] {
		t.Run(s.name, func(t *testing.T) {
			w := liNew(t)
			var got []string
			for _, ev := range []struct{ id, network, url, typ, extra string }{
				{"interception-job-21.0", "N21", liSevenData, "XHR", ""},
				{"interception-job-22.0", "N22", liVaultData, "XHR", ""},
				{"interception-job-23.0", "N23", liSevenURL, "Document", ""},
				{"interception-job-24.0", "N24", liSevenData, "XHR", auth},
			} {
				v := w.intercepted(s.id, ev.id, ev.network, ev.url, ev.typ, ev.extra)
				if v.forward != nil || len(v.page) != 0 {
					t.Fatalf("the client saw a protected interception: %s %q", v.forward, v.page)
				}
				for _, m := range v.send {
					if strings.Contains(string(m), `"Network.continueInterceptedRequest"`) {
						got = append(got, string(m))
					}
				}
			}
			if want := liOwnFallback(s.id, 1879048193); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the guard's own legacy settling changed:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			_, clientBefore, _ := w.wire()
			for n := int64(1879048193); n <= 1879048196; n++ {
				if v := w.in(`{"id":` + fmt.Sprint(n) + `,"result":{}` + liSession(s.id) + `}`); v.forward != nil || len(v.page) != 0 {
					t.Fatalf("the guard's own call answer reached the client: %s", v.forward)
				}
			}
			if _, client, _ := w.wire(); len(client) != len(clientBefore) {
				t.Fatalf("the client got %q", client[len(clientBefore):])
			}
		})
	}
}

func TestLegacyInterceptionGuardOverflowContinueUnchanged(t *testing.T) {
	w := liNew(t)
	w.g.mu.Lock()
	w.g.heldPauseCount = maxHeldPauses
	w.g.mu.Unlock()
	v := w.in(liInterceptedEvent(liPageSession, "interception-job-31.0", "N31", liDataURL, "XHR", ""))
	want := `{"id":1879048193,"method":"Network.continueInterceptedRequest","params":{"interceptionId":"interception-job-31.0"},"sessionId":"` + liPageSession + `"}`
	if v.forward != nil || len(v.send) != 1 || string(v.send[0]) != want {
		t.Fatalf("the guard's own overflow continue changed: forward=%s send=%q", v.forward, v.send)
	}
}

func TestLegacyInterceptionClientAfterGuardSettle(t *testing.T) {
	for _, s := range liSessions[:3] {
		t.Run(s.name, func(t *testing.T) {
			w := liNew(t)
			w.intercepted(s.id, "interception-job-21.0", "N21", liSevenData, "XHR", "")
			w.intercepted(s.id, "interception-job-23.0", "N23", liSevenURL, "Document", "")
			for i, id := range []string{"interception-job-21.0", "interception-job-23.0"} {
				for j, f := range liForms(id)[3:] {
					w.mustRefuse(int64(500+i*50+j), s.id, f)
				}
			}
		})
	}
	t.Run("overflow", func(t *testing.T) {
		w := liNew(t)
		w.g.mu.Lock()
		w.g.heldPauseCount = maxHeldPauses
		w.g.mu.Unlock()
		w.in(liInterceptedEvent(liPageSession, "interception-job-31.0", "N31", liDataURL, "XHR", ""))
		for j, f := range liForms("interception-job-31.0")[3:] {
			w.mustRefuse(int64(600+j), liPageSession, f)
		}
	})
}

func liOwnFallback(session string, first int64) []string {
	s := liSession(session)
	return []string{
		`{"id":` + fmt.Sprint(first) + `,"method":"Network.continueInterceptedRequest","params":{"interceptionId":"interception-job-21.0"}` + s + `}`,
		`{"id":` + fmt.Sprint(first+1) + `,"method":"Network.continueInterceptedRequest","params":{"interceptionId":"interception-job-22.0"}` + s + `}`,
		`{"id":` + fmt.Sprint(first+2) + `,"method":"Network.continueInterceptedRequest","params":{"errorReason":"BlockedByClient","interceptionId":"interception-job-23.0"}` + s + `}`,
		`{"id":` + fmt.Sprint(first+3) + `,"method":"Network.continueInterceptedRequest","params":{"authChallengeResponse":{"response":"Default"},"interceptionId":"interception-job-24.0"}` + s + `}`,
	}
}

var (
	liStockChromePuppeteer = []string{
		`{"id":11,"method":"Network.enable","params":{},"sessionId":"S-LI-PAGE"}`,
		`{"id":12,"method":"Fetch.enable","params":{"handleAuthRequests":true,"patterns":[{"urlPattern":"*"}]},"sessionId":"S-LI-PAGE"}`,
		`{"id":13,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-1.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":14,"method":"Fetch.fulfillRequest","params":{"body":"b2s=","requestId":"interception-job-2.0","responseCode":200,"responseHeaders":[{"name":"Content-Type","value":"text/plain"}]},"sessionId":"S-LI-PAGE"}`,
		`{"id":15,"method":"Fetch.failRequest","params":{"errorReason":"Failed","requestId":"interception-job-3.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":16,"method":"Fetch.getResponseBody","params":{"requestId":"interception-job-4.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":17,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-4.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":18,"method":"Fetch.disable","params":{},"sessionId":"S-LI-PAGE"}`,
	}
	liStockChromePlaywright = []string{
		`{"id":11,"method":"Network.enable","params":{},"sessionId":"S-LI-PAGE"}`,
		`{"id":12,"method":"Fetch.enable","params":{"handleAuthRequests":true,"patterns":[{"requestStage":"Request","urlPattern":"*"},{"requestStage":"Response","urlPattern":"*"}]},"sessionId":"S-LI-PAGE"}`,
		`{"id":13,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-1.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":14,"method":"Fetch.fulfillRequest","params":{"body":"b2s=","requestId":"interception-job-2.0","responseCode":200,"responseHeaders":[{"name":"Content-Type","value":"text/plain"}]},"sessionId":"S-LI-PAGE"}`,
		`{"id":15,"method":"Fetch.failRequest","params":{"errorReason":"Failed","requestId":"interception-job-3.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":16,"method":"Fetch.getResponseBody","params":{"requestId":"interception-job-4.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":17,"method":"Fetch.continueRequest","params":{"requestId":"interception-job-4.0"},"sessionId":"S-LI-PAGE"}`,
		`{"id":18,"method":"Fetch.disable","params":{},"sessionId":"S-LI-PAGE"}`,
	}
	liStockClient = []string{
		`{"id":11,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"id":12,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"method":"Fetch.requestPaused","sessionId":"S-LI-PAGE","params":{"requestId":"interception-job-1.0","request":{"url":"https://page.ordinary.test/data?1","method":"GET","headers":{}},"frameId":"LI-PAGE","resourceType":"XHR","networkId":"N1"}}`,
		`{"id":13,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"method":"Fetch.requestPaused","sessionId":"S-LI-PAGE","params":{"requestId":"interception-job-2.0","request":{"url":"https://page.ordinary.test/data?2","method":"GET","headers":{}},"frameId":"LI-PAGE","resourceType":"XHR","networkId":"N2"}}`,
		`{"id":14,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"method":"Fetch.requestPaused","sessionId":"S-LI-PAGE","params":{"requestId":"interception-job-3.0","request":{"url":"https://page.ordinary.test/data?3","method":"GET","headers":{}},"frameId":"LI-PAGE","resourceType":"XHR","networkId":"N3"}}`,
		`{"id":15,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"method":"Fetch.requestPaused","sessionId":"S-LI-PAGE","params":{"requestId":"interception-job-4.0","request":{"url":"https://page.ordinary.test/data?4","method":"GET","headers":{}},"frameId":"LI-PAGE","resourceType":"XHR","networkId":"N4","responseStatusCode":200,"responseHeaders":[{"name":"Content-Type","value":"text/plain"}]}}`,
		`{"id":16,"result":{"body":"b2s=","base64Encoded":true},"sessionId":"S-LI-PAGE"}`,
		`{"id":17,"result":{},"sessionId":"S-LI-PAGE"}`,
		`{"id":18,"result":{},"sessionId":"S-LI-PAGE"}`,
	}
)
