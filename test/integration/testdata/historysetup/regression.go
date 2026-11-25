package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	hsTarget    = "HS-PAGE"
	hsClient    = "HS-CLIENT"
	hsNav       = "HS-GUARD"
	hsAttachID  = 70
	hsOrdinary  = "https://ordinary.test/page"
	hsSeven     = "https://seven.sliccy.ai/"
	hsMissing   = "https://seven.sliccy.ai/missing"
	hsOrdMiss   = "https://ordinary.test/missing"
	hsChromeErr = "chrome-error://chromewebdata/"

	hsTextHistory   = "the target's history may restore a protected document"
	hsTextGuard     = "slicc can't keep seven's origin out of this target"
	hsTextErrorPage = "the target shows Chrome's error page for a protected URL, which names it; slicc only lets a page navigate, reload or close it"
	hsTextTarget    = "target is on seven's origin"
)

type hsChrome struct {
	t          *testing.T
	g          *guard
	typ        string
	url        string
	origin     string
	unreach    string
	loader     string
	title      string
	frameURL   string
	frameOrig  string
	history    []map[string]any
	historyAlt []map[string]any
	current    int
	currentAlt int
	hold       bool
	holdInfo   bool
	skipInfo   bool
	asked      bool
	kids       map[string]map[string]any
	held       [][]byte
	heldInfo   [][]byte
	next       int64
	client     []string
	guardOut   []string
	logs       []string
}

func hsNewChrome(t *testing.T) *hsChrome {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	c := &hsChrome{t: t, typ: "page", url: hsOrdinary, origin: "https://ordinary.test", loader: "L0", title: "page", next: 100, kids: map[string]map[string]any{}}
	c.g = newGuard(o, func(format string, args ...any) { c.logs = append(c.logs, fmt.Sprintf(format, args...)) })
	c.g.relaySession = "RELAY"
	c.history = []map[string]any{
		{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
	}
	return c
}

func (c *hsChrome) protectedEntry() {
	c.history = []map[string]any{
		{"id": 1, "url": hsSeven, "userTypedURL": hsSeven, "title": "seven", "transitionType": "typed"},
		{"id": 2, "url": c.url, "userTypedURL": c.url, "title": "page", "transitionType": "link"},
	}
	c.current = 1
}

func hsOutbound(raw []byte) (id int64, method, session string, params map[string]any) {
	var m struct {
		ID        *int64         `json:"id"`
		Method    string         `json:"method"`
		SessionID string         `json:"sessionId"`
		Params    map[string]any `json:"params"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.ID != nil {
		id = *m.ID
	}
	return id, m.Method, m.SessionID, m.Params
}

func (c *hsChrome) take(v browserVerdict) {
	if v.forward != nil {
		c.client = append(c.client, string(v.forward))
	}
	for _, p := range v.page {
		c.client = append(c.client, string(p))
	}
	c.outbound(v.send)
}

func hsReply(id int64, session string, result any) []byte {
	m := map[string]any{"id": id, "result": result}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *hsChrome) frame() map[string]any {
	url, origin := c.url, c.origin
	if c.frameURL != "" || c.frameOrig != "" {
		url, origin = c.frameURL, c.frameOrig
	}
	f := map[string]any{"id": hsTarget, "loaderId": c.loader, "url": url, "securityOrigin": origin, "mimeType": "text/html"}
	if c.unreach != "" {
		f["unreachableUrl"] = c.unreach
	}
	return f
}

func (c *hsChrome) info() map[string]any {
	return map[string]any{"targetId": hsTarget, "type": c.typ, "url": c.url, "title": c.title, "browserContextId": "CTX", "attached": true}
}

func (c *hsChrome) outbound(send [][]byte) {
	for _, raw := range send {
		c.guardOut = append(c.guardOut, string(raw))
		id, method, session, params := hsOutbound(raw)
		target, onGuard := c.guardTarget(session)
		switch method {
		case "Target.attachToTarget":
			if session != "RELAY" {
				continue
			}
			want, _ := params["targetId"].(string)
			if want == hsTarget {
				c.feed(hsReply(id, session, map[string]any{"sessionId": hsNav}))
			} else if c.kids[want] != nil {
				c.feed(hsReply(id, session, map[string]any{"sessionId": "G-" + want}))
			}
		case "Page.getNavigationHistory":
			entries, idx := c.history, c.current
			if c.asked && c.historyAlt != nil {
				entries, idx = c.historyAlt, c.currentAlt
			}
			c.asked = true
			c.feed(hsReply(id, session, map[string]any{"currentIndex": idx, "entries": entries}))
		case "Target.getTargetInfo":
			want, _ := params["targetId"].(string)
			if want != hsTarget {
				if k := c.kids[want]; k != nil {
					c.feed(hsReply(id, session, map[string]any{"targetInfo": k}))
				}
				continue
			}
			if c.holdInfo {
				c.heldInfo = append(c.heldInfo, raw)
				continue
			}
			if c.skipInfo && c.asked {
				continue
			}
			c.feed(hsReply(id, session, map[string]any{"targetInfo": c.info()}))
		case "Page.getFrameTree":
			if c.hold && target == hsTarget {
				c.held = append(c.held, raw)
				continue
			}
			c.feed(hsReply(id, session, map[string]any{"frameTree": map[string]any{"frame": c.frameOf(target)}}))
		case "Target.detachFromTarget", "Runtime.runIfWaitingForDebugger":
		default:
			if id != 0 && onGuard {
				c.feed(hsReply(id, session, map[string]any{}))
			}
		}
	}
}

func (c *hsChrome) guardTarget(session string) (string, bool) {
	if session == hsNav {
		return hsTarget, true
	}
	if id, ok := strings.CutPrefix(session, "G-"); ok {
		return id, true
	}
	return "", false
}

func (c *hsChrome) frameOf(target string) map[string]any {
	if target == hsTarget || target == "" {
		return c.frame()
	}
	k := c.kids[target]
	if k == nil {
		return map[string]any{}
	}
	url, _ := k["url"].(string)
	return map[string]any{"id": target, "parentId": hsTarget, "loaderId": "K-" + target, "url": url, "securityOrigin": hsOriginOf(url), "mimeType": "text/html"}
}

func (c *hsChrome) answerHeld(fail bool) {
	for round := 0; len(c.held) > 0 && round < 16; round++ {
		held := c.held
		c.held = nil
		for _, raw := range held {
			id, _, session, _ := hsOutbound(raw)
			if fail {
				c.feed(encode(map[string]any{"id": id, "sessionId": session, "error": map[string]any{"code": -32000, "message": "Frame tree is not available"}}))
				continue
			}
			c.feed(hsReply(id, session, map[string]any{"frameTree": map[string]any{"frame": c.frame()}}))
		}
	}
}

func (c *hsChrome) answerHeldInfo(url string) {
	c.t.Helper()
	info := c.info()
	if url != "" {
		info["url"] = url
	}
	for round := 0; len(c.heldInfo) > 0 && round < 16; round++ {
		held := c.heldInfo
		c.heldInfo = nil
		c.holdInfo = false
		for _, raw := range held {
			id, _, session, _ := hsOutbound(raw)
			c.feed(hsReply(id, session, map[string]any{"targetInfo": info}))
		}
	}
}

func hsProbeTreeOrdinary(marker string) map[string]any {
	text := "ordinary body"
	if marker != "" {
		text = marker
	}
	return map[string]any{
		"root": map[string]any{
			"nodeId": 1, "nodeName": "#document", "documentURL": hsOrdinary, "baseURL": hsOrdinary,
			"children": []any{
				map[string]any{"nodeId": 2, "nodeName": "#text", "nodeValue": text},
			},
		},
	}
}

func hsProbeTreeProtected(marker string) map[string]any {
	text := "ordinary body"
	if marker != "" {
		text = marker
	}
	return map[string]any{
		"root": map[string]any{
			"nodeId": 1, "nodeName": "#document", "documentURL": hsOrdinary, "baseURL": hsOrdinary,
			"children": []any{
				map[string]any{
					"nodeId": 2, "nodeName": "IFRAME", "frameId": "HS-IFRAME",
					"attributes": []any{"src", hsSeven + "embed"},
					"contentDocument": map[string]any{
						"nodeId": 3, "nodeName": "#document", "documentURL": hsSeven + "embed", "baseURL": hsSeven + "embed",
						"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": text}},
					},
				},
			},
		},
	}
}

func hsProbeTreeOOPIFSrc(marker string) map[string]any {
	text := "ordinary body"
	if marker != "" {
		text = marker
	}
	return map[string]any{
		"root": map[string]any{
			"nodeId": 1, "nodeName": "#document", "documentURL": hsOrdinary, "baseURL": hsOrdinary,
			"children": []any{
				map[string]any{
					"nodeId": 2, "nodeName": "IFRAME", "frameId": "HS-OOPIF",
					"attributes": []any{"src", hsSeven + "embed"},
				},
				map[string]any{"nodeId": 3, "nodeName": "#text", "nodeValue": text},
			},
		},
	}
}

func hsDocumentUpdated() string {
	return string(encode(map[string]any{"method": "DOM.documentUpdated", "sessionId": hsClient, "params": map[string]any{}}))
}

func (c *hsChrome) admitProbeHeld(tree map[string]any) (id int64, treeAnswer []byte) {
	c.t.Helper()
	c.holdInfo = true
	raw, out, _ := c.call(hsProbe)
	if out != "forward "+raw {
		c.t.Fatalf("probe not forwarded: %s", out)
	}
	id, _, _, _ = hsOutbound([]byte(raw))
	if _, ok := c.g.docIdentityCalls[callKey{hsClient, id}]; !ok {
		c.t.Fatal("admitted probe must enter docIdentityCalls (#863)")
	}
	treeAnswer = hsReply(id, hsClient, tree)
	c.client = nil
	c.feed(treeAnswer)
	if len(c.heldInfo) == 0 {
		c.t.Fatal("probe reply must arm Target.getTargetInfo for the identity hold")
	}
	if len(c.client) != 0 {
		c.t.Fatalf("probe reply must stay held until getTargetInfo settles: %q", c.client)
	}
	if len(c.g.docIdentityHolds) == 0 {
		c.t.Fatal("probe reply must sit in docIdentityHolds")
	}
	return id, treeAnswer
}

func (c *hsChrome) admitProbeRefused(tree map[string]any, marker string) int64 {
	c.t.Helper()
	c.holdInfo = true
	raw, out, _ := c.call(hsProbe)
	if out != "forward "+raw {
		c.t.Fatalf("probe not forwarded: %s", out)
	}
	id, _, _, _ := hsOutbound([]byte(raw))
	if _, ok := c.g.docIdentityCalls[callKey{hsClient, id}]; !ok {
		c.t.Fatal("admitted probe must enter docIdentityCalls (#863)")
	}
	c.client = nil
	c.feed(hsReply(id, hsClient, tree))
	if len(c.heldInfo) != 0 {
		c.t.Fatal("protected child must refuse before the identity hold")
	}
	if len(c.g.docIdentityHolds) != 0 {
		c.t.Fatal("protected child must not enter docIdentityHolds")
	}
	if _, ok := c.g.docIdentityCalls[callKey{hsClient, id}]; ok {
		c.t.Fatal("refusal must drop the probe from docIdentityCalls")
	}
	want := strings.TrimPrefix(hsRefusal(id, hsTextTarget), "reply ")
	updated := hsDocumentUpdated()
	switch {
	case len(c.client) == 2 && c.client[0] == want && c.client[1] == updated:
	case len(c.client) == 1 && c.client[0] == want:
		c.t.Fatalf("mark first latch must synthesize DOM.documentUpdated; got only the refusal: %q", c.client)
	default:
		c.t.Fatalf("want refusal then content-free DOM.documentUpdated; got %q", c.client)
	}
	for _, msg := range c.client {
		if marker != "" && strings.Contains(msg, marker) {
			c.t.Fatalf("refusal path must not carry marker: %s", msg)
		}
	}
	page := c.g.targets[hsTarget]
	if page == nil || !page.childDocDOMBlocked {
		c.t.Fatalf("protected child must mark the tab: %+v", page)
	}
	return id
}

func (c *hsChrome) topFrameNavigated(loader string) {
	c.t.Helper()
	c.loader = loader
	c.client = nil
	c.feed(hsEvent(hsClient, "Page.frameNavigated", map[string]any{
		"frame": map[string]any{"id": hsTarget, "loaderId": loader, "url": hsOrdinary, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"},
	}))
}

func (c *hsChrome) seedRootDocument() {
	c.t.Helper()
	loader := c.loader
	if loader == "" {
		loader = "L0"
	}
	c.topFrameNavigated(loader)
	page := c.g.targets[hsTarget]
	if page == nil || page.document != loader {
		c.t.Fatalf("seedRootDocument must commit loader %s: %+v", loader, page)
	}
}

func (c *hsChrome) clearChildDocMark() {
	c.t.Helper()
	page := c.g.targets[hsTarget]
	if page == nil || !page.childDocDOMBlocked {
		c.t.Fatalf("clearChildDocMark needs a marked tab: %+v", page)
	}
	same := page.document
	if same == "" {
		c.t.Fatal("clearChildDocMark needs a seeded page.document so same-loader keep is observable (commitRootDocumentLocked clears on the empty→loader commit)")
	}
	c.topFrameNavigated(same)
	page = c.g.targets[hsTarget]
	if page == nil || !page.childDocDOMBlocked {
		c.t.Fatalf("same-loader frameNavigated must keep the child-doc mark (commitRootDocumentLocked): %+v", page)
	}
	c.topFrameNavigated(same + "-NEXT")
	page = c.g.targets[hsTarget]
	if page == nil || page.childDocDOMBlocked {
		c.t.Fatalf("a new loaderId commit must clear the child-doc mark: %+v", page)
	}
	if nt := c.nt(); nt == nil || !nt.historyLocked {
		c.t.Fatalf("top-level navigation must keep the history lock: %+v", nt)
	}
}

func (c *hsChrome) feed(raw []byte) {
	c.take(c.g.fromBrowser(raw))
}

func hsEvent(session, method string, params map[string]any) []byte {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	return encode(m)
}

func (c *hsChrome) attach() {
	t := c.t
	t.Helper()
	c.feed(hsEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": c.info()}))
	pv := c.g.fromCommand(&command{id: hsAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": hsTarget, "flatten": true}}, hsAttachID, true)
	if pv.reply != nil {
		t.Fatalf("client attach refused: %s", pv.reply)
	}
	c.outbound(pv.send)
	c.feed(hsEvent("", "Target.attachedToTarget", map[string]any{"sessionId": hsClient, "waitingForDebugger": false, "targetInfo": c.info()}))
	c.feed(hsReply(hsAttachID, "", map[string]any{"sessionId": hsClient}))
	if c.g.sessions[hsClient] == nil {
		t.Fatalf("client session not admitted: %v", c.client)
	}
	c.client = nil
}

func (c *hsChrome) nt() *navTarget {
	return c.g.navTargets[hsTarget]
}

func hsLockedTab(t *testing.T) *hsChrome {
	t.Helper()
	c := hsNewChrome(t)
	c.protectedEntry()
	c.attach()
	c.requireLocked()
	return c
}

func hsCleanTab(t *testing.T) *hsChrome {
	t.Helper()
	c := hsNewChrome(t)
	c.attach()
	nt := c.nt()
	if nt == nil || !nt.done || nt.failed || nt.historyLocked {
		t.Fatalf("clean tab not set up: %+v", nt)
	}
	return c
}

func (c *hsChrome) requireLocked() {
	c.t.Helper()
	nt := c.nt()
	if nt == nil || !nt.done || nt.failed || !nt.historyLocked || (nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)) || nt.errorURL != "" {
		c.t.Fatalf("history-locked ordinary tab not set up: %+v", nt)
	}
	if nt.setupProtectedSeen {
		c.t.Fatalf("history-locked ordinary tab must not be latched yet: seenSeq=%d", nt.setupProtectedSeq)
	}
	if s := c.g.sessions[hsClient]; s == nil || s.typ != c.typ || s.refused {
		c.t.Fatalf("client session not usable: %+v", s)
	}
}

func (c *hsChrome) requireLatched() {
	c.t.Helper()
	if nt := c.nt(); nt == nil || !nt.setupProtectedSeen {
		c.t.Fatalf("want setupProtectedSeen: %+v", nt)
	}
}

type hsCall struct {
	name   string
	method string
	params any
}

func (c *hsChrome) raw(method string, params any) string {
	c.next++
	m := map[string]any{"id": c.next, "method": method, "sessionId": hsClient}
	if params != nil {
		m["params"] = params
	}
	return string(encode(m))
}

func (c *hsChrome) sendRaw(raw string) (string, pageVerdict) {
	cmd, id, ok := parseCommand([]byte(raw))
	v := c.g.fromCommand(cmd, id, ok)
	c.outbound(v.send)
	switch {
	case v.reply != nil:
		return "reply " + string(v.reply), v
	case v.forward != nil:
		return "forward " + string(v.forward), v
	case v.wait != nil:
		return "wait", v
	}
	return "other", v
}

func (c *hsChrome) call(k hsCall) (raw, out string, v pageVerdict) {
	raw = c.raw(k.method, k.params)
	out, v = c.sendRaw(raw)
	return raw, out, v
}

func hsRefusal(id int64, text string) string {
	return fmt.Sprintf(`reply {"error":{"code":-32000,"message":"slicc: refused: %s"},"id":%d,"sessionId":"HS-CLIENT"}`, text, id)
}

func hsFrameChildren() []any {
	return []any{map[string]any{"type": "iframe", "exclude": false}, map[string]any{"exclude": true}}
}

var hsSetup = []hsCall{
	{"bypass", "Network.setBypassServiceWorker", map[string]any{"bypass": true}},
	{"autoattach", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": hsFrameChildren()}},
}

var hsSetupForms = append(append([]hsCall{}, hsSetup...),
	hsCall{"autoattach-no-wait", "Target.setAutoAttach", map[string]any{"autoAttach": true, "flatten": true, "filter": hsFrameChildren()}},
)

var hsProbe = hsCall{"get-document", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true}}

var hsProbeDisable = hsCall{"dom-disable", "DOM.disable", nil}

var hsContent = []hsCall{
	{"evaluate", "Runtime.evaluate", map[string]any{"expression": "document.title"}},
	{"get-document-depth-0", "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}},
	{"get-document-pierce-false", "DOM.getDocument", map[string]any{"depth": -1, "pierce": false}},
	{"dom-enable", "DOM.enable", nil},
	{"dom-disable-alone", "DOM.disable", nil},
	{"snapshot", "DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []any{}}},
	{"screenshot", "Page.captureScreenshot", map[string]any{}},
}

func (c *hsChrome) requireForwarded(k hsCall) {
	c.t.Helper()
	raw, out, v := c.call(k)
	if out != "forward "+raw {
		c.t.Fatalf("%s %s: want forwarded byte for byte; got %s; want forward %s", k.name, k.method, out, raw)
	}
	if len(v.send) != 0 {
		c.t.Fatalf("%s: extra messages to the browser: %q", k.name, v.send)
	}
	id, _, _, _ := hsOutbound([]byte(raw))
	answer := string(hsReply(id, hsClient, map[string]any{}))
	c.client = nil
	c.feed([]byte(answer))
	if len(c.client) != 1 || c.client[0] != answer {
		c.t.Fatalf("%s: the browser's answer did not reach the client byte for byte: %q want %s", k.name, c.client, answer)
	}
	c.client = nil
}

func (c *hsChrome) requireRefused(k hsCall, text string) {
	c.t.Helper()
	raw, out, v := c.call(k)
	id, _, _, _ := hsOutbound([]byte(raw))
	if want := hsRefusal(id, text); out != want {
		c.t.Fatalf("%s %s: got %s; want %s", k.name, k.method, out, want)
	}
	if len(v.send) != 0 {
		c.t.Fatalf("%s: a refused command sent messages to the browser: %q", k.name, v.send)
	}
}

func TestHistorySetupAdmittedOnLockedTab(t *testing.T) {
	for _, k := range hsSetupForms {
		t.Run(k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireForwarded(k)
			c.requireLocked()
		})
	}
}

func TestHistorySetupProbeAdmittedOnLockedTab(t *testing.T) {
	c := hsLockedTab(t)
	c.requireForwarded(hsProbe)
	if !c.g.historySetupProbe[hsClient] {
		t.Fatal("admitted probe must latch the session for DOM.disable")
	}
	c.requireForwarded(hsProbeDisable)
	if c.g.historySetupProbe[hsClient] {
		t.Fatal("DOM.disable must clear the probe latch")
	}
	c.requireLocked()
}

func TestHistorySetupDisableWithoutProbeRefused(t *testing.T) {
	c := hsLockedTab(t)
	c.requireRefused(hsProbeDisable, hsTextHistory)
	c.requireLocked()
}

func TestHistorySetupProbeLatchRequiresHistoryLock(t *testing.T) {
	c := hsCleanTab(t)
	c.requireForwarded(hsProbe)
	if c.g.historySetupProbe[hsClient] {
		t.Fatal("a tab that is not history-locked must not latch DOM.disable from the ownership probe")
	}
}

func TestHistorySetupProbeLatchClearedOnForget(t *testing.T) {
	c := hsLockedTab(t)
	c.requireForwarded(hsProbe)
	if !c.g.historySetupProbe[hsClient] {
		t.Fatal("admitted probe must latch the session")
	}
	c.feed(hsEvent("", "Target.detachedFromTarget", map[string]any{"sessionId": hsClient, "targetId": hsTarget}))
	if c.g.historySetupProbe[hsClient] {
		t.Fatal("forgetting the session must clear the probe latch")
	}
}

func TestHistorySetupConditions(t *testing.T) {
	states := []struct {
		name  string
		setup func(t *testing.T) *hsChrome
		want  map[string]string
	}{
		{"guard-not-done", func(t *testing.T) *hsChrome {
			c := hsNewChrome(t)
			c.protectedEntry()
			c.skipInfo = true
			c.attach()
			if nt := c.nt(); nt == nil || nt.done || !nt.historyLocked {
				t.Fatalf("want a locked nav record that is not done: %+v", nt)
			}
			return c
		}, map[string]string{"*": hsTextGuard}},
		{"guard-failed", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			c.feed(hsEvent("RELAY", "Target.detachedFromTarget", map[string]any{"sessionId": hsNav, "targetId": hsTarget}))
			if nt := c.nt(); nt == nil || !nt.failed || !nt.historyLocked {
				t.Fatalf("want a failed locked nav record: %+v", nt)
			}
			return c
		}, map[string]string{"*": hsTextGuard}},
		{"no-nav-record", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			delete(c.g.navTargets, hsTarget)
			return c
		}, map[string]string{"*": hsTextGuard}},
		{"confirm-open", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			c.hold = true
			c.clientRestore(hsOrdinary, "https://ordinary.test", "L1")
			if nt := c.nt(); nt.confirm == nil || nt.confirm.done {
				t.Fatalf("want an open confirm: %+v", nt.confirm)
			}
			return c
		}, map[string]string{"*": hsTextGuard}},
		{"confirm-failed", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			c.hold = true
			c.clientRestore(hsOrdinary, "https://ordinary.test", "L1")
			c.answerHeld(true)
			if nt := c.nt(); nt.confirm == nil || !nt.confirm.done || !nt.confirm.failed {
				t.Fatalf("want a failed confirm: %+v", nt.confirm)
			}
			return c
		}, map[string]string{"*": hsTextGuard}},
		{"protected-error-page", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			c.failLoad(hsMissing)
			if !c.g.errorPageSessionLocked(hsClient) {
				t.Fatalf("want a protected error page")
			}
			return c
		}, map[string]string{"*": hsTextErrorPage, "autoattach": hsTextHistory}},
		{"ordinary-error-page", func(t *testing.T) *hsChrome {
			c := hsLockedTab(t)
			c.failLoad(hsOrdMiss)
			if nt := c.nt(); nt.errorURL != hsOrdMiss || c.g.errorPageSessionLocked(hsClient) || !nt.historyLocked {
				t.Fatalf("want a locked tab on an ordinary error page: %+v", nt)
			}
			return c
		}, map[string]string{"*": hsTextHistory}},
		{"webview-target", func(t *testing.T) *hsChrome {
			c := hsNewChrome(t)
			c.typ = "webview"
			c.protectedEntry()
			c.attach()
			c.requireLocked()
			return c
		}, map[string]string{"*": hsTextHistory}},
	}
	for _, st := range states {
		for _, k := range hsSetup {
			t.Run(st.name+"/"+k.name, func(t *testing.T) {
				c := st.setup(t)
				text, ok := st.want[k.name]
				if !ok {
					text = st.want["*"]
				}
				c.requireRefused(k, text)
			})
		}
	}
	for _, k := range hsSetupForms {
		t.Run("not-history-locked/"+k.name, func(t *testing.T) {
			hsCleanTab(t).requireForwarded(k)
		})
	}
}

func (c *hsChrome) clientRestore(url, origin, loader string) {
	c.url, c.origin, c.unreach, c.loader = url, origin, "", loader
	c.feed(hsEvent(hsClient, "Page.frameNavigated", map[string]any{"frame": c.frame(), "type": "BackForwardCacheRestore"}))
}

func (c *hsChrome) guardRestore(url string) {
	c.url, c.origin, c.unreach, c.loader = url, "https://seven.sliccy.ai", "", "L9"
	c.feed(hsEvent(hsNav, "Page.frameNavigated", map[string]any{"frame": c.frame(), "type": "BackForwardCacheRestore"}))
}

func (c *hsChrome) infoChanged(url, title string) {
	c.url, c.title = url, title
	c.feed(hsEvent("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": c.info()}))
}

func (c *hsChrome) failLoad(failed string) {
	p := map[string]any{"frameId": hsTarget, "loaderId": "L1", "url": failed, "navigationType": "differentDocument"}
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", p))
	c.feed(hsEvent(hsClient, "Page.frameStartedNavigating", p))
	if c.g.o.Loaded(failed) {
		c.feed(hsEvent(hsNav, "Fetch.requestPaused", map[string]any{
			"requestId": "F-L1", "networkId": "L1", "frameId": hsTarget, "resourceType": "Document",
			"request": map[string]any{"url": failed, "method": "GET", "headers": map[string]any{}},
		}))
	}
	c.feed(hsEvent(hsNav, "Network.loadingFailed", map[string]any{"requestId": "L1", "type": "Document", "errorText": "net::ERR_BLOCKED_BY_CLIENT", "canceled": false}))
	c.url, c.origin, c.unreach, c.loader = hsChromeErr, "://", failed, "L1"
	nav := map[string]any{"frame": c.frame(), "type": "Navigation"}
	c.feed(hsEvent(hsNav, "Page.frameNavigated", nav))
	c.feed(hsEvent(hsClient, "Page.frameNavigated", nav))
	c.infoChanged(hsChromeErr, protect.Host(failed))
	stop := map[string]any{"frameId": hsTarget}
	c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", stop))
	c.feed(hsEvent(hsClient, "Page.frameStoppedLoading", stop))
	c.answerHeld(false)
	nt := c.nt()
	if nt == nil || !nt.done || nt.failed || (nt.confirm != nil && (!nt.confirm.done || nt.confirm.failed)) || nt.errorURL != failed {
		c.t.Fatalf("error page not settled: %+v", nt)
	}
}

func TestHistorySetupShadowedConditions(t *testing.T) {
	states := []struct {
		name string
		mut  func(nt *navTarget)
	}{
		{"not-done", func(nt *navTarget) { nt.done = false }},
		{"failed", func(nt *navTarget) { nt.failed = true }},
		{"confirm-open", func(nt *navTarget) { nt.confirm = &navTarget{ready: make(chan struct{})} }},
		{"confirm-failed", func(nt *navTarget) { nt.confirm = &navTarget{ready: make(chan struct{}), done: true, failed: true} }},
		{"error-url", func(nt *navTarget) { nt.errorURL = hsOrdMiss }},
		{"setup-protected-seen", func(nt *navTarget) { nt.setupProtectedSeen = true; nt.setupProtectedSeq = 1 }},
	}
	for _, st := range states {
		for _, k := range append(append([]hsCall{}, hsSetup...), hsProbe) {
			t.Run(st.name+"/"+k.name, func(t *testing.T) {
				c := hsLockedTab(t)
				st.mut(c.nt())
				cmd, _, ok := parseCommand([]byte(c.raw(k.method, k.params)))
				if !ok {
					t.Fatal("parse")
				}
				c.g.mu.Lock()
				got := c.g.conformanceLocked(cmd)
				c.g.mu.Unlock()
				if got != hsTextHistory {
					t.Fatalf("conformance on a locked tab with %s admitted %s: %q", st.name, k.method, got)
				}
			})
		}
	}
	t.Run("browser-flagged-session", func(t *testing.T) {
		for _, k := range append(append([]hsCall{}, hsSetup...), hsProbe) {
			c := hsLockedTab(t)
			c.g.browserSessions[hsClient] = true
			cmd, _, _ := parseCommand([]byte(c.raw(k.method, k.params)))
			c.g.mu.Lock()
			got := c.g.conformanceLocked(cmd)
			c.g.mu.Unlock()
			if got != hsTextHistory {
				t.Fatalf("%s on a browser-flagged locked session: %q", k.method, got)
			}
		}
	})
}

var hsParamsRefused = []hsCall{
	{"bypass-false", "Network.setBypassServiceWorker", map[string]any{"bypass": false}},
	{"bypass-string", "Network.setBypassServiceWorker", map[string]any{"bypass": "true"}},
	{"bypass-number", "Network.setBypassServiceWorker", map[string]any{"bypass": 1}},
	{"bypass-null", "Network.setBypassServiceWorker", map[string]any{"bypass": nil}},
	{"bypass-missing", "Network.setBypassServiceWorker", map[string]any{}},
	{"bypass-no-params", "Network.setBypassServiceWorker", nil},
	{"bypass-extra-key", "Network.setBypassServiceWorker", map[string]any{"bypass": true, "x": 1}},
	{"autoattach-off", "Target.setAutoAttach", map[string]any{"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true, "filter": hsFrameChildren()}},
	{"autoattach-string", "Target.setAutoAttach", map[string]any{"autoAttach": "true", "waitForDebuggerOnStart": false, "flatten": true, "filter": hsFrameChildren()}},
	{"autoattach-flatten-false", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": false, "filter": hsFrameChildren()}},
	{"autoattach-flatten-missing", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "filter": hsFrameChildren()}},
	{"autoattach-no-filter", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}},
	{"autoattach-bare", "Target.setAutoAttach", map[string]any{"autoAttach": true, "flatten": true}},
	{"autoattach-wait-true", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true, "filter": hsFrameChildren()}},
	{"autoattach-wait-string", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": "false", "flatten": true, "filter": hsFrameChildren()}},
	{"autoattach-wait-null", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": nil, "flatten": true, "filter": hsFrameChildren()}},
	{"autoattach-unknown-key", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": hsFrameChildren(), "windowOpen": true}},
	{"autoattach-filter-all", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{}}}},
	{"autoattach-filter-empty", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{}}},
	{"autoattach-filter-workers", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{"type": "iframe", "exclude": false}, map[string]any{"type": "worker", "exclude": false}, map[string]any{"exclude": true}}}},
	{"autoattach-filter-reordered", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{"exclude": true}, map[string]any{"type": "iframe", "exclude": false}}}},
	{"autoattach-filter-page", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{"type": "page", "exclude": false}, map[string]any{"exclude": true}}}},
	{"autoattach-filter-extra-entry-key", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{"type": "iframe", "exclude": false, "x": 1}, map[string]any{"exclude": true}}}},
	{"autoattach-filter-exclude-missing", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []any{map[string]any{"type": "iframe"}, map[string]any{"exclude": true}}}},
	{"autoattach-filter-object", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": map[string]any{"type": "iframe", "exclude": false}}},
	{"autoattach-filter-string", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": "iframe"}},
	{"autoattach-filter-null", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": nil}},
	{"autoattach-no-params", "Target.setAutoAttach", nil},
}

var hsMethodsRefused = []hsCall{
	{"focus", "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}},
	{"set-discover", "Target.setDiscoverTargets", map[string]any{"discover": true}},
	{"attach-related", "Target.autoAttachRelated", map[string]any{"targetId": hsTarget, "waitForDebuggerOnStart": false}},
	{"emulated-media", "Emulation.setEmulatedMedia", map[string]any{"media": "print"}},
	{"touch", "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true}},
	{"device-metrics", "Emulation.setDeviceMetricsOverride", map[string]any{"width": 1, "height": 1, "deviceScaleFactor": 1, "mobile": false}},
	{"network-cookies", "Network.getCookies", map[string]any{}},
	{"lifecycle", "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
	{"runtime-evaluate", "Runtime.evaluate", map[string]any{"expression": "1"}},
	{"dom-get-document-depth-0", "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}},
	{"dom-get-document-pierce-false", "DOM.getDocument", map[string]any{"depth": -1, "pierce": false}},
	{"dom-get-document-depth-string", "DOM.getDocument", map[string]any{"depth": "-1", "pierce": true}},
	{"dom-get-document-pierce-string", "DOM.getDocument", map[string]any{"depth": -1, "pierce": "true"}},
	{"dom-get-document-backend-node", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true, "backendNodeId": 1}},
	{"dom-get-document-node-id", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true, "nodeId": 1}},
	{"dom-get-document-extra-key", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true, "x": 1}},
	{"dom-get-document-missing-pierce", "DOM.getDocument", map[string]any{"depth": -1}},
	{"dom-get-document-missing-depth", "DOM.getDocument", map[string]any{"pierce": true}},
	{"dom-get-document-empty", "DOM.getDocument", map[string]any{}},
	{"dom-enable", "DOM.enable", nil},
	{"dom-disable-alone", "DOM.disable", nil},
	{"dom-query", "DOM.querySelector", map[string]any{"nodeId": 1, "selector": "body"}},
	{"snapshot", "DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []any{}}},
}

func TestHistorySetupParamsRefused(t *testing.T) {
	for _, k := range append(append([]hsCall{}, hsParamsRefused...), hsMethodsRefused...) {
		t.Run(k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireRefused(k, hsTextHistory)
		})
	}
	t.Run("dom-get-document-depth-minus-one-point-zero", func(t *testing.T) {
		c := hsLockedTab(t)
		c.next++
		raw := fmt.Sprintf(`{"id":%d,"method":"DOM.getDocument","sessionId":%q,"params":{"depth":-1.0,"pierce":true}}`, c.next, hsClient)
		out, v := c.sendRaw(raw)
		want := strings.TrimPrefix(hsRefusal(int64(c.next), hsTextHistory), "reply ")
		if out != "reply "+want {
			t.Fatalf("depth -1.0 (json.Number) must be history-refused: out=%s send=%q", out, v.send)
		}
	})
}

func TestHistorySetupDisableOtherSessionRefused(t *testing.T) {
	c := hsLockedTab(t)
	c.requireForwarded(hsProbe)
	const other = "HS-CLIENT-2"
	c.feed(hsEvent("", "Target.attachedToTarget", map[string]any{"sessionId": other, "waitingForDebugger": false, "targetInfo": c.info()}))
	if c.g.sessions[other] == nil {
		t.Fatal("second page session must register")
	}
	c.next++
	raw := fmt.Sprintf(`{"id":%d,"method":"DOM.disable","sessionId":%q}`, c.next, other)
	out, v := c.sendRaw(raw)
	want := fmt.Sprintf(`{"error":{"code":-32000,"message":"slicc: refused: %s"},"id":%d,"sessionId":%q}`, hsTextHistory, c.next, other)
	if out != "reply "+want {
		t.Fatalf("DOM.disable on a session without the probe latch must be refused: out=%s send=%q", out, v.send)
	}
	if c.g.historySetupProbe[hsClient] != true {
		t.Fatal("other-session disable must not clear the probing session's latch")
	}
	c.requireForwarded(hsProbeDisable)
}

func TestHistorySetupDuplicateKeys(t *testing.T) {
	for _, row := range []struct {
		name, method, params, canonical string
		admit                           bool
	}{
		{"bypass-last-true", "Network.setBypassServiceWorker", `{"bypass":false,"bypass":true}`, `{"bypass":true}`, true},
		{"bypass-last-false", "Network.setBypassServiceWorker", `{"bypass":true,"bypass":false}`, "", false},
		{"wait-last-false", "Target.setAutoAttach", `{"autoAttach":true,"flatten":true,"waitForDebuggerOnStart":true,"waitForDebuggerOnStart":false,"filter":[{"type":"iframe","exclude":false},{"exclude":true}]}`, `{"autoAttach":true,"filter":[{"exclude":false,"type":"iframe"},{"exclude":true}],"flatten":true,"waitForDebuggerOnStart":false}`, true},
		{"filter-last-exact", "Target.setAutoAttach", `{"autoAttach":true,"flatten":true,"filter":[{}],"filter":[{"type":"iframe","exclude":false},{"exclude":true}]}`, `{"autoAttach":true,"filter":[{"exclude":false,"type":"iframe"},{"exclude":true}],"flatten":true}`, true},
		{"filter-last-all", "Target.setAutoAttach", `{"autoAttach":true,"flatten":true,"filter":[{"type":"iframe","exclude":false},{"exclude":true}],"filter":[{}]}`, "", false},
		{"probe-depth-last-minus-one", "DOM.getDocument", `{"depth":0,"pierce":true,"depth":-1}`, `{"depth":-1,"pierce":true}`, true},
		{"probe-pierce-last-true", "DOM.getDocument", `{"depth":-1,"pierce":false,"pierce":true}`, `{"depth":-1,"pierce":true}`, true},
		{"probe-depth-last-zero", "DOM.getDocument", `{"depth":-1,"pierce":true,"depth":0}`, "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.next++
			raw := fmt.Sprintf(`{"id":%d,"method":%q,"sessionId":%q,"params":%s}`, c.next, row.method, hsClient, row.params)
			out, _ := c.sendRaw(raw)
			if !row.admit {
				if want := hsRefusal(c.next, hsTextHistory); out != want {
					t.Fatalf("got %s want %s", out, want)
				}
				return
			}
			want := fmt.Sprintf(`forward {"id":%d,"method":%q,"params":%s,"sessionId":%q}`, c.next, row.method, row.canonical, hsClient)
			if out != want {
				t.Fatalf("the browser must get the guard's reading of the command, not the client's bytes\n got: %s\nwant: %s", out, want)
			}
		})
	}
}

func TestHistorySetupContentStaysRefused(t *testing.T) {
	for _, k := range hsContent {
		t.Run(k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireRefused(k, hsTextHistory)
			for _, s := range hsSetup {
				c.requireForwarded(s)
			}
			c.requireForwarded(hsProbe)
			c.requireForwarded(hsProbeDisable)
			c.requireRefused(k, hsTextHistory)
			c.requireLocked()
		})
	}
}

func TestHistorySetupStockTranscript(t *testing.T) {
	calls := append(append([]hsCall{
		{"fetch", "Fetch.enable", map[string]any{"patterns": []any{map[string]any{"urlPattern": "*"}}}},
		{"network", "Network.enable", map[string]any{}},
		{"page", "Page.enable", nil},
		{"frame-tree", "Page.getFrameTree", nil},
	}, hsSetupForms...), hsContent...)
	calls = append(calls, hsParamsRefused...)
	c := hsCleanTab(t)
	for _, k := range calls {
		c.requireForwarded(k)
	}
	if c.nt().historyLocked {
		t.Fatal("the clean tab locked")
	}
}

func TestHistorySetupLockedTranscript(t *testing.T) {
	c := hsLockedTab(t)
	for _, k := range []hsCall{
		{"fetch", "Fetch.enable", map[string]any{"patterns": []any{map[string]any{"urlPattern": "*"}}}},
		{"network", "Network.enable", map[string]any{}},
		{"page", "Page.enable", nil},
		{"frame-tree", "Page.getFrameTree", nil},
	} {
		raw, out, _ := c.call(k)
		if out != "forward "+raw {
			t.Fatalf("%s: %s", k.name, out)
		}
	}
}

func TestHistorySetupProtectedTwin(t *testing.T) {
	for _, k := range append(append(append([]hsCall{}, hsSetup...), hsProbe, hsProbeDisable), hsContent...) {
		t.Run(k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.protectCurrent()
			c.requireRefused(k, hsTextTarget)
		})
	}
}

func TestHistorySetupProbeReplyScrub(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	id, answer := c.admitProbeHeld(hsProbeTreeOrdinary(marker))
	scrubbedDirect := string(c.g.scrub(append([]byte(nil), answer...)))
	if !strings.Contains(scrubbedDirect, "ordinary.test") {
		t.Fatalf("scrub must keep the ordinary top documentURL: %s", scrubbedDirect)
	}
	if !strings.Contains(scrubbedDirect, marker) {
		t.Fatalf("ordinary probe tree must keep its marker through scrub: %s", scrubbedDirect)
	}
	c.client = nil
	c.answerHeldInfo(hsOrdinary)
	if len(c.client) == 0 {
		t.Fatal("ordinary unchanged getTargetInfo must deliver the held probe reply")
	}
	delivered := string(c.g.scrub([]byte(c.client[0])))
	if !strings.Contains(delivered, marker) {
		t.Fatalf("delivered ordinary probe reply must carry the marker: %s", delivered)
	}
	if !strings.Contains(c.client[0], fmt.Sprintf(`"id":%d`, id)) && !strings.Contains(c.client[0], fmt.Sprintf(`"id": %d`, id)) {
		t.Fatalf("delivered reply must carry the probe id: %s", c.client[0])
	}
}

func TestHistorySetupProbeOOPIFSrcScrubbed(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	id, answer := c.admitProbeHeld(hsProbeTreeOOPIFSrc(marker))
	scrubbedDirect := string(c.g.scrub(append([]byte(nil), answer...)))
	if strings.Contains(scrubbedDirect, "/embed") || strings.Contains(scrubbedDirect, hsSeven+"embed") {
		t.Fatalf("write-time scrub must cut the OOPIF src path: %s", scrubbedDirect)
	}
	if !strings.Contains(scrubbedDirect, marker) {
		t.Fatalf("ordinary marker text must survive scrub: %s", scrubbedDirect)
	}
	c.client = nil
	c.answerHeldInfo(hsOrdinary)
	if len(c.client) != 1 {
		t.Fatalf("want one delivered OOPIF probe reply; got %q", c.client)
	}
	delivered := string(c.g.scrub([]byte(c.client[0])))
	if strings.Contains(delivered, "/embed") || strings.Contains(delivered, "seven.sliccy.ai/embed") {
		t.Fatalf("delivered OOPIF probe must not carry a protected path: %s", delivered)
	}
	if !strings.Contains(delivered, marker) {
		t.Fatalf("delivered OOPIF probe must keep the ordinary marker: %s", delivered)
	}
	if !strings.Contains(c.client[0], fmt.Sprintf(`"id":%d`, id)) && !strings.Contains(c.client[0], fmt.Sprintf(`"id": %d`, id)) {
		t.Fatalf("delivered reply must carry the probe id: %s", c.client[0])
	}
}

func TestHistorySetupProbeIdentityOrdinaryDelivered(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	id, _ := c.admitProbeHeld(hsProbeTreeOrdinary(marker))
	taint := c.g.docIdentityTaint[hsTarget]
	c.client = nil
	c.answerHeldInfo(hsOrdinary)
	if len(c.client) != 1 {
		t.Fatalf("want one delivered probe reply; got %q", c.client)
	}
	if strings.Contains(c.client[0], `"error"`) {
		t.Fatalf("ordinary unchanged post-read must deliver, not refuse: %s", c.client[0])
	}
	if !strings.Contains(c.client[0], marker) {
		t.Fatalf("ordinary delivered probe must carry the marker: %s", c.client[0])
	}
	if c.g.docIdentityTaint[hsTarget] != taint {
		t.Fatalf("ordinary settle must not bump taint")
	}
	if c.g.sessions[hsClient] == nil || c.g.sessions[hsClient].refused {
		t.Fatal("ordinary settle must keep the client session")
	}
	_ = id
}

func TestHistorySetupProbeProtectedChildRefused(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	c.seedRootDocument()
	_ = c.admitProbeRefused(hsProbeTreeProtected(marker), marker)
	c.requireRefused(hsProbe, hsTextTarget)
	c.requireForwarded(hsProbeDisable)
	if c.g.historySetupProbe[hsClient] {
		t.Fatal("DOM.disable must clear the probe latch")
	}
	if nt := c.nt(); nt == nil || !nt.historyLocked {
		t.Fatalf("tab must stay history-locked after the mark: %+v", nt)
	}
	c.clearChildDocMark()
	_ = c.admitProbeRefused(hsProbeTreeProtected(marker), marker)
}

func TestHistorySetupProbeIdentityTaintRace(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	id, _ := c.admitProbeHeld(hsProbeTreeOrdinary(marker))
	c.g.mu.Lock()
	before := c.g.docIdentityTaint[hsTarget]
	c.g.noteDocIdentityWatchURLLocked(hsTarget, hsSeven)
	after := c.g.docIdentityTaint[hsTarget]
	c.g.mu.Unlock()
	if after <= before {
		t.Fatalf("a protected sighting on the guard's target must bump docIdentityTaint: before=%d after=%d", before, after)
	}
	c.client = nil
	c.guardOut = nil
	c.answerHeldInfo(hsOrdinary)
	want := strings.TrimPrefix(hsRefusal(id, hsTextTarget), "reply ")
	if len(c.client) != 1 || c.client[0] != want {
		t.Fatalf("tainted hold must refuse with target text:\n got: %q\nwant: %s", c.client, want)
	}
	for _, msg := range c.client {
		if strings.Contains(msg, marker) {
			t.Fatalf("tainted hold must not deliver the held marker: %s", msg)
		}
	}
	if c.g.sessions[hsClient] == nil || !c.g.sessions[hsClient].refused {
		t.Fatal("tainted hold must cut the client session")
	}
	detached := false
	for _, o := range c.guardOut {
		_, method, _, params := hsOutbound([]byte(o))
		if method == "Target.detachFromTarget" && params["sessionId"] == hsClient {
			detached = true
		}
	}
	if !detached {
		t.Fatalf("tainted hold must detach the client session: %q", c.guardOut)
	}
}

func TestHistorySetupProbeIdentityProtectedInfo(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	id, _ := c.admitProbeHeld(hsProbeTreeOrdinary(marker))
	c.client = nil
	c.guardOut = nil
	c.answerHeldInfo(hsSeven)
	want := strings.TrimPrefix(hsRefusal(id, hsTextTarget), "reply ")
	if len(c.client) != 1 || c.client[0] != want {
		t.Fatalf("protected getTargetInfo must refuse:\n got: %q\nwant: %s", c.client, want)
	}
	for _, msg := range c.client {
		if strings.Contains(msg, marker) {
			t.Fatalf("protected getTargetInfo must not deliver the held marker: %s", msg)
		}
	}
	if c.g.sessions[hsClient] == nil || !c.g.sessions[hsClient].refused {
		t.Fatal("protected getTargetInfo must cut the client session")
	}
}

func TestHistorySetupProbeIdentityRelayLess(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	c := hsLockedTab(t)
	c.holdInfo = true
	raw, out, _ := c.call(hsProbe)
	if out != "forward "+raw {
		t.Fatalf("probe not forwarded: %s", out)
	}
	id, _, _, _ := hsOutbound([]byte(raw))
	c.g.relaySession = ""
	c.client = nil
	c.feed(hsReply(id, hsClient, hsProbeTreeOrdinary(marker)))
	if len(c.heldInfo) != 0 {
		t.Fatalf("relay-less hold must not ask getTargetInfo: %d", len(c.heldInfo))
	}
	if len(c.client) != 1 {
		t.Fatalf("relay-less hold must answer the client once; got %q", c.client)
	}
	if !strings.Contains(c.client[0], "Execution context was destroyed") {
		t.Fatalf("relay-less hold must use the retriable text: %s", c.client[0])
	}
	if strings.Contains(c.client[0], marker) {
		t.Fatalf("relay-less hold must not deliver the held marker: %s", c.client[0])
	}
}

var hsDOMProtocolEvents = []string{
	"DOM.attributeModified",
	"DOM.adoptedStyleSheetsModified",
	"DOM.attributeRemoved",
	"DOM.characterDataModified",
	"DOM.childNodeCountUpdated",
	"DOM.childNodeInserted",
	"DOM.childNodeRemoved",
	"DOM.distributedNodesUpdated",
	"DOM.documentUpdated",
	"DOM.inlineStyleInvalidated",
	"DOM.pseudoElementAdded",
	"DOM.topLayerElementsUpdated",
	"DOM.scrollableFlagUpdated",
	"DOM.adRelatedStateUpdated",
	"DOM.affectedByStartingStylesFlagUpdated",
	"DOM.pseudoElementRemoved",
	"DOM.setChildNodes",
	"DOM.shadowRootPopped",
	"DOM.shadowRootPushed",
}

func TestHistorySetupProbeEventsDropped(t *testing.T) {
	c := hsLockedTab(t)
	c.requireForwarded(hsProbe)
	for _, method := range hsDOMProtocolEvents {
		t.Run(method, func(t *testing.T) {
			c.client = nil
			c.feed(hsEvent(hsClient, method, map[string]any{"nodeId": 1}))
			for _, msg := range c.client {
				if strings.Contains(msg, method) {
					t.Fatalf("history-locked page must drop every DOM.* event: %s", msg)
				}
			}
		})
	}
	for _, method := range []string{"Accessibility.nodesUpdated", "Accessibility.loadComplete"} {
		t.Run(method, func(t *testing.T) {
			c.client = nil
			c.feed(hsEvent(hsClient, method, map[string]any{}))
			for _, msg := range c.client {
				if strings.Contains(msg, method) {
					t.Fatalf("history-locked page must still drop Accessibility content events: %s", msg)
				}
			}
		})
	}
	c.requireForwarded(hsProbeDisable)
}

func TestHistorySetupDisableAfterProtectAdmitted(t *testing.T) {
	c := hsLockedTab(t)
	c.requireForwarded(hsProbe)
	if !c.g.domEnabled[hsClient] {
		t.Fatal("admitted probe must mark the DOM agent enabled")
	}
	c.nt().setupProtectedSeen = true
	c.nt().setupProtectedSeq = 1
	c.requireForwarded(hsProbeDisable)
	if c.g.domEnabled[hsClient] {
		t.Fatal("DOM.disable after a protected sighting must turn the agent off")
	}
	if c.g.historySetupProbe[hsClient] {
		t.Fatal("DOM.disable must clear the probe latch")
	}
	c.requireRefused(hsProbeDisable, hsTextHistory)
	c.requireLatched()
	if nt := c.nt(); nt == nil || !nt.historyLocked {
		t.Fatalf("tab must stay history-locked: %+v", nt)
	}
}

func (c *hsChrome) protectCurrent() {
	c.t.Helper()
	c.guardOut = nil
	c.guardRestore(hsSeven)
	c.clientRestore(hsSeven, "https://seven.sliccy.ai", "L9")
	c.infoChanged(hsSeven, "seven")
	c.answerHeld(false)
	s := c.g.sessions[hsClient]
	if s == nil || !s.refused || !c.g.sessionRefusedLocked(hsClient) {
		c.t.Fatalf("the client session was not refused after the restore: %+v logs=%q", s, c.logs)
	}
	detached := false
	for _, o := range c.guardOut {
		_, method, _, params := hsOutbound([]byte(o))
		if method == "Target.detachFromTarget" && params["sessionId"] == hsClient {
			detached = true
		}
	}
	if !detached {
		c.t.Fatalf("the guard did not detach the client session: %q", c.guardOut)
	}
}

func TestHistorySetupRestoreOrders(t *testing.T) {
	const marker = "HS-PROBE-MARKER"
	for _, k := range append(append([]hsCall{}, hsSetup...), hsProbe) {
		t.Run("before-restore/"+k.name, func(t *testing.T) {
			if k.method == "DOM.getDocument" {
				for _, arm := range []struct {
					name string
					tree map[string]any
					hold bool
				}{
					{"ordinary-marker", hsProbeTreeOrdinary(marker), true},
					{"protected-child", hsProbeTreeProtected(marker), false},
				} {
					t.Run(arm.name, func(t *testing.T) {
						c := hsLockedTab(t)
						c.holdInfo = true
						raw, out, _ := c.call(k)
						if out != "forward "+raw {
							t.Fatalf("not forwarded: %s", out)
						}
						c.protectCurrent()
						id, _, _, _ := hsOutbound([]byte(raw))
						c.client = nil
						c.heldInfo = nil
						c.feed(hsReply(id, hsClient, arm.tree))
						if arm.hold {
							if len(c.g.docIdentityHolds) == 0 {
								t.Fatal("ordinary tree must still enter the identity hold after protect")
							}
							c.answerHeldInfo(hsSeven)
						} else if len(c.g.docIdentityHolds) != 0 {
							t.Fatal("protected child must refuse before the identity hold")
						}
						want := strings.TrimPrefix(hsRefusal(id, hsTextTarget), "reply ")
						updated := hsDocumentUpdated()
						for _, msg := range c.client {
							if strings.Contains(msg, marker) {
								t.Fatalf("restore refuse must not carry marker: %s", msg)
							}
						}
						switch {
						case arm.hold && len(c.client) == 1 && c.client[0] == want:
						case !arm.hold && len(c.client) == 2 && c.client[0] == want && c.client[1] == updated:
						case !arm.hold && len(c.client) == 1 && c.client[0] == want:
							t.Fatalf("protected-child restore must synthesize DOM.documentUpdated onto the refused session; got %q", c.client)
						default:
							t.Fatalf("the answer after the restore:\n got: %q\nwant refusal%s", c.client, map[bool]string{false: " then DOM.documentUpdated"}[arm.hold])
						}
						c.requireRefused(k, hsTextTarget)
					})
				}
				return
			}
			c := hsLockedTab(t)
			raw, out, _ := c.call(k)
			if out != "forward "+raw {
				t.Fatalf("not forwarded: %s", out)
			}
			c.protectCurrent()
			id, _, _, _ := hsOutbound([]byte(raw))
			c.client = nil
			c.feed(hsReply(id, hsClient, map[string]any{}))
			want := strings.TrimPrefix(hsRefusal(id, hsTextTarget), "reply ")
			if len(c.client) != 1 || c.client[0] != want {
				t.Fatalf("the answer after the restore:\n got: %q\nwant: %s", c.client, want)
			}
			c.requireRefused(k, hsTextTarget)
		})
		for _, mid := range []struct {
			name string
			text string
			step func(c *hsChrome)
		}{
			{"mid-restore-guard-commit", hsTextTarget, func(c *hsChrome) { c.guardRestore(hsSeven) }},
			{"mid-restore-guard-commit-origin", hsTextHistory, func(c *hsChrome) { c.guardRestore("about:blank") }},
			{"mid-restore-client-commit", hsTextTarget, func(c *hsChrome) { c.clientRestore(hsSeven, "https://seven.sliccy.ai", "L9") }},
			{"mid-restore-target-info", hsTextGuard, func(c *hsChrome) { c.infoChanged(hsSeven, "seven") }},
		} {
			t.Run(mid.name+"/"+k.name, func(t *testing.T) {
				c := hsLockedTab(t)
				c.hold = true
				mid.step(c)
				c.requireRefused(k, mid.text)
			})
		}
		t.Run("in-flight-restore/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			p := map[string]any{"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument"}
			c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", p))
			c.requireRefused(k, hsTextHistory)
		})
		t.Run("after-restore/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.protectCurrent()
			c.requireRefused(k, hsTextTarget)
		})
		t.Run("guard-lost/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireForwarded(k)
			c.feed(hsEvent("RELAY", "Target.detachedFromTarget", map[string]any{"sessionId": hsNav, "targetId": hsTarget}))
			c.requireRefused(k, hsTextGuard)
		})
		t.Run("relay-lost/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireForwarded(k)
			c.feed(hsEvent("", "Target.detachedFromTarget", map[string]any{"sessionId": "RELAY"}))
			c.requireRefused(k, "slicc lost track of the browser's targets")
		})
		t.Run("same-value/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireForwarded(k)
			c.requireForwarded(k)
			c.requireForwarded(k)
			c.requireLocked()
		})
	}
}

func TestHistorySetupRestoreExplicitAttach(t *testing.T) {
	for _, row := range []struct {
		name   string
		commit bool
		want   string
	}{
		{"before-restore-events", false, "reply=held"},
		{"after-guard-commit", true, "cmd=refused"},
	} {
		t.Run(row.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.hold = true
			if raw, out, _ := c.call(hsSetup[1]); out != "forward "+raw {
				t.Fatalf("setAutoAttach: %s", out)
			}
			if row.commit {
				c.guardRestore(hsSeven)
			}
			c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
			pv := c.g.fromCommand(&command{id: 900, method: "Target.attachToTarget", params: map[string]any{"targetId": "NEW", "flatten": true}}, 900, true)
			c.outbound(pv.send)
			var got string
			switch {
			case pv.reply != nil:
				got = "cmd=refused"
			case pv.forward != nil:
				c.client = nil
				c.feed(hsEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-NEW", "waitingForDebugger": false, "targetInfo": c.kids["NEW"]}))
				c.feed(hsReply(900, "", map[string]any{"sessionId": "S-NEW"}))
				switch {
				case c.attachedForwarded("NEW"):
					got = "reply=forward"
				case c.g.held["S-NEW"] != nil:
					got = "reply=held"
				case c.g.hidden["S-NEW"]:
					got = "reply=hidden"
				case c.g.sessions["S-NEW"] != nil && c.g.sessionRefusedLocked("S-NEW"):
					got = "reply=refused"
				default:
					t.Fatalf("unclassified attach outcome: client=%q held=%v hidden=%v session=%v", c.client, c.g.held["S-NEW"] != nil, c.g.hidden["S-NEW"], c.g.sessions["S-NEW"])
				}
			case pv.wait != nil:
				got = "cmd=wait"
			default:
				t.Fatalf("unclassified attach verdict: reply=%v forward=%v wait=%v", pv.reply != nil, pv.forward != nil, pv.wait != nil)
			}
			if got != row.want {
				t.Fatalf("got %s want %s; client=%q logs=%q held=%v", got, row.want, c.client, c.logs, c.g.held["S-NEW"])
			}
		})
	}
}

func hsOriginOf(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "null"
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host
}

func (c *hsChrome) kidInfo(id, typ, url string) map[string]any {
	info := map[string]any{"targetId": id, "type": typ, "url": url, "title": "", "browserContextId": "CTX", "attached": true}
	if typ == "iframe" {
		info["parentFrameId"] = hsTarget
		info["parentId"] = hsTarget
	} else {
		info["parentId"] = hsTarget
	}
	return info
}

func (c *hsChrome) kidAnnounced(id, typ, url string, clientPage bool) {
	info := c.kidInfo(id, typ, url)
	c.kids[id] = info
	if typ == "iframe" {
		p := map[string]any{"frameId": id, "parentFrameId": hsTarget}
		c.feed(hsEvent(hsNav, "Page.frameAttached", p))
		if clientPage {
			c.feed(hsEvent(hsClient, "Page.frameAttached", p))
		}
	}
	c.feed(hsEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": info}))
}

func (c *hsChrome) kidAttached(id string) {
	c.feed(hsEvent(hsClient, "Target.attachedToTarget", map[string]any{"sessionId": "S-" + id, "waitingForDebugger": false, "targetInfo": c.kids[id]}))
}

func (c *hsChrome) attachedForwarded(id string) bool {
	for _, m := range c.client {
		if strings.Contains(m, `"method":"Target.attachedToTarget"`) && strings.Contains(m, `"sessionId":"S-`+id+`"`) {
			return true
		}
	}
	return false
}

func (c *hsChrome) heldChild(id string) bool {
	return c.g.held["S-"+id] != nil
}

func (c *hsChrome) kidUsable(id string) string {
	c.next++
	raw := fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"expression":"document.title"},"sessionId":"S-%s"}`, c.next, id)
	out, _ := c.sendRaw(raw)
	if strings.HasPrefix(out, "forward") {
		return "forwarded"
	}
	var r struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(strings.TrimPrefix(out, "reply ")), &r)
	return "answer: " + r.Error.Message
}

func hsRequireHistoryHold(t *testing.T, c *hsChrome, id string) {
	t.Helper()
	if c.attachedForwarded(id) {
		t.Fatalf("auto-attached child must be held, not delivered: %q", c.client)
	}
	if !c.heldChild(id) {
		t.Fatalf("child session was not held: held=%v sessions=%v hidden=%v", c.g.held["S-"+id] != nil, c.g.sessions["S-"+id] != nil, c.g.hidden["S-"+id])
	}
	if got := c.kidUsable(id); got != "answer: "+missingSessionMessage {
		t.Fatalf("held child usable: %s", got)
	}
	held := false
	for _, line := range c.logs {
		if strings.Contains(line, "holding a child of a history-locked page") {
			held = true
		}
	}
	if !held {
		t.Fatalf("missing hold log: %q", c.logs)
	}
}

func TestHistorySetupAutoAttachHold(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	hsRequireHistoryHold(t, c, "NEW")
	c.url, c.origin, c.loader = hsOrdinary, "https://ordinary.test", "L-confirm"
	c.answerHeld(false)
	if !c.attachedForwarded("NEW") {
		t.Fatalf("child not delivered after ordinary confirm: client=%q logs=%q", c.client, c.logs)
	}
	if got := c.kidUsable("NEW"); got != "forwarded" {
		t.Fatalf("delivered child not usable: %s", got)
	}
}

func TestHistorySetupAutoAttachNoFilterRefused(t *testing.T) {
	noFilter := hsCall{"autoattach-no-filter", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}}
	bare := hsCall{"autoattach-bare", "Target.setAutoAttach", map[string]any{"autoAttach": true, "flatten": true}}
	for _, k := range []hsCall{noFilter, bare} {
		t.Run(k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireRefused(k, hsTextHistory)
		})
	}
	for _, typ := range []string{"iframe", "worker", "service_worker", "shared_worker", "auction_worklet"} {
		t.Run(typ, func(t *testing.T) {
			c := hsLockedTab(t)
			c.requireRefused(noFilter, hsTextHistory)
		})
	}
}

func TestHistorySetupAutoAttachHoldHiddenWhenProtected(t *testing.T) {
	for _, kid := range []struct {
		id, typ, url string
	}{
		{"NEW", "iframe", "https://third.test/embed"},
		{"NEW-W", "worker", "https://third.test/w.js"},
	} {
		t.Run(kid.typ, func(t *testing.T) {
			c := hsLockedTab(t)
			c.hold = true
			c.requireForwarded(hsSetup[1])
			c.client = nil
			c.kidAnnounced(kid.id, kid.typ, kid.url, true)
			c.kidAttached(kid.id)
			if c.attachedForwarded(kid.id) {
				t.Fatal("child delivered before confirm")
			}
			c.guardRestore(hsSeven)
			c.clientRestore(hsSeven, "https://seven.sliccy.ai", "L9")
			c.infoChanged(hsSeven, "seven")
			c.answerHeld(false)
			if c.attachedForwarded(kid.id) {
				t.Fatalf("%s must stay hidden after protect: %q", kid.typ, c.client)
			}
			sid := "S-" + kid.id
			if !c.g.hidden[sid] {
				t.Fatalf("%s want hidden after protect: session=%v", kid.typ, c.g.sessions[sid])
			}
			if c.g.sessions[sid] != nil && !c.g.sessionRefusedLocked(sid) {
				t.Fatalf("%s want refused or absent session after protect: %+v", kid.typ, c.g.sessions[sid])
			}
		})
	}
}

func TestHistorySetupProtectedSightingLatch(t *testing.T) {
	for _, k := range hsSetup {
		t.Run("in-flight/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			p := map[string]any{"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument"}
			c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", p))
			c.requireLatched()
			c.requireRefused(k, hsTextHistory)
		})
		t.Run("target-info/"+k.name, func(t *testing.T) {
			c := hsLockedTab(t)
			c.hold = true
			c.infoChanged(hsSeven, "seven")
			if !c.nt().setupProtectedSeen {
				t.Fatal("target-info sighting must set setupProtectedSeen")
			}
			c.requireLatched()
			c.requireRefused(k, hsTextGuard)
		})
	}
}

func TestHistorySetupNavEventLatchPaths(t *testing.T) {
	t.Run("non-fetch/frameStartedNavigating", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
			"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("non-fetch/frameNavigated-document", func(t *testing.T) {
		c := hsLockedTab(t)
		c.hold = true
		c.guardRestore(hsSeven)
		c.requireLatched()
	})
	t.Run("non-fetch/frameNavigated-origin", func(t *testing.T) {
		c := hsLockedTab(t)
		c.hold = true
		c.guardRestore("about:blank")
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("non-fetch/frameNavigated-errorURL", func(t *testing.T) {
		c := hsLockedTab(t)
		c.url, c.origin, c.unreach, c.loader = hsChromeErr, "://", hsMissing, "L-err"
		c.feed(hsEvent(hsNav, "Page.frameNavigated", map[string]any{"frame": c.frame(), "type": "Navigation"}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextErrorPage)
		c.requireRefused(hsSetup[1], hsTextHistory)
	})
	t.Run("non-fetch/stopped-loading-keeps-latch", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
			"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
		}))
		c.requireLatched()
		c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
		if c.nt().navigationURL != "" {
			t.Fatalf("navigationURL should clear on stop: %q", c.nt().navigationURL)
		}
		c.requireLatched()
		c.requireRefused(hsSetup[1], hsTextHistory)
	})
	t.Run("non-fetch/loadingFailed-keeps-nav-latch", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
			"frameId": hsTarget, "loaderId": "L-load", "url": hsSeven, "navigationType": "historyDifferentDocument",
		}))
		c.requireLatched()
		c.feed(hsEvent(hsNav, "Network.loadingFailed", map[string]any{
			"requestId": "L-load", "type": "Document", "errorText": "net::ERR_ABORTED", "canceled": true,
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("fetch/requestPaused-Document", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Fetch.requestPaused", map[string]any{
			"requestId": "F-L9", "networkId": "L9", "frameId": hsTarget, "resourceType": "Document",
			"request": map[string]any{"url": hsSeven, "method": "GET", "headers": map[string]any{}},
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("fetch/requestPaused-unparseable", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed([]byte(`{"method":"Fetch.requestPaused","sessionId":"HS-GUARD","params":[]}`))
		c.requireLatched()
		c.requireRefused(hsSetup[1], hsTextHistory)
	})
	t.Run("fetch/requestPaused-ordinary-continue", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Fetch.requestPaused", map[string]any{
			"requestId": "F-ord", "networkId": "Nord", "frameId": hsTarget, "resourceType": "Document",
			"request": map[string]any{"url": hsOrdinary, "method": "GET", "headers": map[string]any{}},
		}))
		if c.nt().setupProtectedSeen {
			t.Fatal("ordinary Document pause must not latch")
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("documentsOnly/fetch-Document", func(t *testing.T) {
		c := hsLockedTab(t)
		c.nt().documentsAsked = true
		c.nt().documentsOnly = true
		c.feed(hsEvent(hsNav, "Fetch.requestPaused", map[string]any{
			"requestId": "F-doc", "networkId": "Ndoc", "frameId": "CHILD-FRAME", "resourceType": "Document",
			"request": map[string]any{"url": hsSeven, "method": "GET", "headers": map[string]any{}},
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("observe/targetInfoChanged", func(t *testing.T) {
		c := hsLockedTab(t)
		c.hold = true
		c.infoChanged(hsSeven, "seven")
		c.requireLatched()
	})
	t.Run("non-fetch/unparseable-frameStarted", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed([]byte(`{"method":"Page.frameStartedNavigating","sessionId":"HS-GUARD","params":[]}`))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("non-fetch/unparseable-frameNavigated", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed([]byte(`{"method":"Page.frameNavigated","sessionId":"HS-GUARD","params":[]}`))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("non-fetch/empty-committed-url", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Page.frameNavigated", map[string]any{
			"frame": map[string]any{"id": hsTarget, "loaderId": "L-empty", "url": "", "securityOrigin": "https://ordinary.test", "mimeType": "text/html"},
			"type":  "Navigation",
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("non-fetch/empty-committed-origin", func(t *testing.T) {
		c := hsLockedTab(t)
		c.feed(hsEvent(hsNav, "Page.frameNavigated", map[string]any{
			"frame": map[string]any{"id": hsTarget, "loaderId": "L-empty-o", "url": hsOrdinary, "securityOrigin": "", "mimeType": "text/html"},
			"type":  "Navigation",
		}))
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
}

func hsGuardAttach(t *testing.T, c *hsChrome) {
	t.Helper()
	c.feed(hsEvent("RELAY", "Target.targetCreated", map[string]any{"targetInfo": c.info()}))
	c.g.mu.Lock()
	send := c.g.touchLocked(hsTarget, "page")
	c.g.mu.Unlock()
	c.outbound(send)
}

func (c *hsChrome) sawOutbound(method string) bool {
	for _, raw := range c.guardOut {
		_, m, _, _ := hsOutbound([]byte(raw))
		if m == method {
			return true
		}
	}
	return false
}

func (c *hsChrome) countOutbound(method string) int {
	n := 0
	for _, raw := range c.guardOut {
		_, m, _, _ := hsOutbound([]byte(raw))
		if m == method {
			n++
		}
	}
	return n
}

func (c *hsChrome) countScheduleAndSettle(t *testing.T) *int {
	t.Helper()
	n := 0
	c.g.scheduleInitial = func(nt *navTarget) {
		n++
	}
	return &n
}

func (c *hsChrome) requireOneExtraHistoryNoSchedule(t *testing.T, scheduled *int) {
	t.Helper()
	if *scheduled != 0 {
		t.Fatalf("chrome-error frame fresh-history path must not call scheduleInitial; scheduled=%d", *scheduled)
	}
	if n := c.countOutbound("Page.getNavigationHistory"); n != 2 {
		t.Fatalf("want exactly one extra Page.getNavigationHistory (total 2); got %d", n)
	}
}

func (c *hsChrome) requireSingleHistoryNoSchedule(t *testing.T, scheduled *int) {
	t.Helper()
	if *scheduled != 0 {
		t.Fatalf("already-locked frame settle must not call scheduleInitial; scheduled=%d", *scheduled)
	}
	if n := c.countOutbound("Page.getNavigationHistory"); n != 1 {
		t.Fatalf("already-locked tab must not take a fresh Page.getNavigationHistory (want 1); got %d", n)
	}
}

func TestHistorySetupInitialLatchDirections(t *testing.T) {
	t.Run("history-only-ordinary-current", func(t *testing.T) {
		c := hsLockedTab(t)
		if c.nt().setupProtectedSeen {
			t.Fatal("history-only protected entries must not latch an ordinary current document")
		}
		c.requireForwarded(hsSetup[0])
		c.requireForwarded(hsSetup[1])
	})
	t.Run("clean-frame-admit", func(t *testing.T) {
		c := hsLockedTab(t)
		if c.nt().setupProtectedSeen {
			t.Fatal("clean initial frame must admit without latch")
		}
		c.requireForwarded(hsSetup[1])
	})
	t.Run("bios-path-sends-getFrameTree", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		hsGuardAttach(t, c)
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("bios protected-history path must send Page.getFrameTree")
		}
		if c.nt().setupProtectedSeen {
			t.Fatal("clean bios frame must admit without latch")
		}
	})
	t.Run("clean-about-blank-settles-without-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = "about:blank"
		c.origin = "://"
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("clean about:blank must settle unlocked at once: %+v", nt)
		}
		if c.sawOutbound("Page.getFrameTree") {
			t.Fatal("clean about:blank must not send Page.getFrameTree")
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("clean-about-srcdoc-settles-without-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = "about:srcdoc"
		c.origin = "://"
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("clean about:srcdoc must settle unlocked at once: %+v", nt)
		}
		if c.sawOutbound("Page.getFrameTree") {
			t.Fatal("clean about:srcdoc must not send Page.getFrameTree")
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("clean-empty-url-reads-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		c.attach()
		nt := c.nt()
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("clean empty target url must send Page.getFrameTree as on main")
		}
		if nt == nil || !nt.done || nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("clean empty url with clean frame must settle unlocked: %+v", nt)
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("locked-inheriting-url-reads-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = "about:blank"
		c.origin = "://"
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		hsGuardAttach(t, c)
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("history-locked inheriting url must still send Page.getFrameTree")
		}
		if c.nt().setupProtectedSeen {
			t.Fatal("clean frame on locked inheriting info must not latch")
		}
	})
	t.Run("locked-empty-url-reads-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		scheduled := c.countScheduleAndSettle(t)
		hsGuardAttach(t, c)
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("history-locked empty url must still send Page.getFrameTree")
		}
		c.requireSingleHistoryNoSchedule(t, scheduled)
		if c.nt().setupProtectedSeen {
			t.Fatal("clean frame on locked empty info must not latch")
		}
	})
	t.Run("locked-frame-one-history", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		scheduled := c.countScheduleAndSettle(t)
		c.attach()
		nt := c.nt()
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("history-locked ordinary tab must still send Page.getFrameTree for latch vs clean")
		}
		c.requireSingleHistoryNoSchedule(t, scheduled)
		if nt == nil || !nt.done || !nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("locked clean frame must settle locked without latch: %+v", nt)
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("protected-current-info", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = hsSeven
		c.origin = "https://seven.sliccy.ai"
		c.title = "seven"
		c.history = []map[string]any{
			{"id": 1, "url": hsSeven, "userTypedURL": hsSeven, "title": "seven", "transitionType": "typed"},
		}
		c.current = 0
		hsGuardAttach(t, c)
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("want settled history-locked tab: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("protected-current-frame", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsSeven
		c.frameOrig = "https://seven.sliccy.ai"
		c.history = []map[string]any{
			{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
		}
		c.current = 0
		hsGuardAttach(t, c)
		nt := c.nt()
		if nt == nil || !nt.historyLocked {
			t.Fatalf("want historyLocked after protected initial frame: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("no-scheduler-unknown-locks", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.history = []map[string]any{
			{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
		}
		c.current = 0
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("no-scheduler unknown must settle historyLocked as on main: %+v", nt)
		}
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("unknown-history-chrome-error-info-locks", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = hsChromeErr
		c.origin = "://"
		c.unreach = hsOrdMiss
		c.history = []map[string]any{}
		c.current = 0
		c.frameURL = hsChromeErr
		c.frameOrig = "://"
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("unknown history + chrome-error info must end locked as on main: %+v", nt)
		}
		if !c.sawOutbound("Page.getFrameTree") {
			t.Fatal("unknown history must reach the frame read (not settle on chrome-error info alone)")
		}
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("unknown-history-chrome-error-frame-locks", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.history = []map[string]any{}
		c.current = 0
		c.frameURL = hsChromeErr
		c.frameOrig = "://"
		c.unreach = hsOrdMiss
		c.attach()
		nt := c.nt()
		if n := c.countOutbound("Page.getNavigationHistory"); n != 1 {
			t.Fatalf("unknown history + chrome-error frame must defer (no F4 fresh history); got %d getNavigationHistory", n)
		}
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("unknown history + chrome-error frame must end locked as on main: %+v", nt)
		}
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
	t.Run("unknown-then-clean-retry-admits", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		scheduled := 0
		var deferred *navTarget
		c.g.scheduleInitial = func(nt *navTarget) {
			scheduled++
			deferred = nt
		}
		hsGuardAttach(t, c)
		if scheduled < 1 || deferred == nil {
			t.Fatal("want scheduleInitial after unknown frame")
		}
		if c.nt().done {
			t.Fatal("deferred tab must not be done before the clean retry")
		}
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		c.url = hsOrdinary
		c.origin = "https://ordinary.test"
		if msg := c.g.retryInitial(deferred); msg != nil {
			c.outbound([][]byte{msg})
		}
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("want settled history-locked tab after clean retry: %+v", nt)
		}
		if nt.setupProtectedSeen {
			t.Fatal("clean answer on retry must admit without latch")
		}
	})
	t.Run("empty-target-url-latches", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		hsGuardAttach(t, c)
		c.requireLatched()
	})
	t.Run("inheriting-target-url-latches", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = "about:blank"
		c.origin = ""
		c.frameURL = "about:blank"
		c.frameOrig = ""
		hsGuardAttach(t, c)
		c.requireLatched()
	})
	t.Run("protected-currentIndex-latches", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = hsOrdinary
		c.origin = "https://ordinary.test"
		c.history = []map[string]any{
			{"id": 1, "url": hsSeven, "userTypedURL": hsSeven, "title": "seven", "transitionType": "typed"},
			{"id": 2, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "link"},
		}
		c.current = 0
		hsGuardAttach(t, c)
		nt := c.nt()
		if nt == nil || !nt.historyLocked {
			t.Fatalf("want historyLocked: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("ordinary-error-page-before-attach-latches", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = hsChromeErr
		c.origin = "://"
		c.unreach = hsOrdMiss
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		scheduled := c.countScheduleAndSettle(t)
		hsGuardAttach(t, c)
		nt := c.nt()
		if *scheduled != 0 {
			t.Fatalf("chrome-error target info must settle without defer; scheduled=%d", *scheduled)
		}
		if nt == nil || !nt.done {
			t.Fatalf("want settled after chrome-error info: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("ordinary-url-chrome-error-frame-settles", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = hsOrdinary
		c.origin = "https://ordinary.test"
		c.frameURL = hsChromeErr
		c.frameOrig = "://"
		c.unreach = hsOrdMiss
		scheduled := c.countScheduleAndSettle(t)
		hsGuardAttach(t, c)
		nt := c.nt()
		c.requireSingleHistoryNoSchedule(t, scheduled)
		if nt == nil || !nt.done {
			t.Fatalf("want settled after chrome-error frame: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("clean-chrome-error-info-admits", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = hsChromeErr
		c.origin = "://"
		c.unreach = hsOrdMiss
		scheduled := c.countScheduleAndSettle(t)
		c.attach()
		nt := c.nt()
		if *scheduled != 0 {
			t.Fatalf("clean-history chrome-error info must settle without defer; scheduled=%d", *scheduled)
		}
		if nt == nil || !nt.done || nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("clean-history chrome-error info must settle unlocked: %+v", nt)
		}
		c.requireForwarded(hsSetup[0])
		c.requireForwarded(hsSetup[1])
	})
	t.Run("clean-incomplete-chrome-error-frame-admits", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsChromeErr
		c.frameOrig = "://"
		c.unreach = hsOrdMiss
		scheduled := c.countScheduleAndSettle(t)
		c.attach()
		nt := c.nt()
		c.requireOneExtraHistoryNoSchedule(t, scheduled)
		if nt == nil || !nt.done || nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("clean-history chrome-error frame must settle unlocked: %+v", nt)
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("clean-then-protected-chrome-error-frame-locks", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsChromeErr
		c.frameOrig = "://"
		c.unreach = hsOrdMiss
		c.history = []map[string]any{
			{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
		}
		c.current = 0
		c.historyAlt = []map[string]any{
			{"id": 1, "url": hsSeven, "userTypedURL": hsSeven, "title": "seven", "transitionType": "typed"},
			{"id": 2, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "link"},
		}
		c.currentAlt = 1
		scheduled := c.countScheduleAndSettle(t)
		c.attach()
		nt := c.nt()
		c.requireOneExtraHistoryNoSchedule(t, scheduled)
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("clean then protected fresh history on chrome-error frame must lock: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("locked-unknown-frame-one-history", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		scheduled := 0
		var deferred *navTarget
		c.g.scheduleInitial = func(nt *navTarget) {
			scheduled++
			deferred = nt
		}
		c.attach()
		for c.nt() != nil && !c.nt().done && deferred != nil && scheduled < 8 {
			if msg := c.g.retryInitial(deferred); msg != nil {
				c.outbound([][]byte{msg})
			} else {
				break
			}
		}
		nt := c.nt()
		if n := c.countOutbound("Page.getNavigationHistory"); n != 1 {
			t.Fatalf("locked unknown frame must not re-read history; got %d getNavigationHistory", n)
		}
		frames := c.countOutbound("Page.getFrameTree")
		if frames < 1 || frames > maxInitialUnknowns {
			t.Fatalf("want 1..%d getFrameTree for locked unknown retries; got %d", maxInitialUnknowns, frames)
		}
		if scheduled != maxInitialUnknowns-1 {
			t.Fatalf("want %d scheduleInitial calls before bound settle; scheduled=%d", maxInitialUnknowns-1, scheduled)
		}
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("want settled history-locked tab after frame-only retries: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("locked-unknown-then-clean-frame-admits", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		scheduled := 0
		var deferred *navTarget
		c.g.scheduleInitial = func(nt *navTarget) {
			scheduled++
			deferred = nt
		}
		c.attach()
		if scheduled < 1 || deferred == nil {
			t.Fatalf("want scheduleInitial after first unknown frame; scheduled=%d done=%v", scheduled, c.nt() != nil && c.nt().done)
		}
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		if msg := c.g.retryInitial(deferred); msg != nil {
			c.outbound([][]byte{msg})
		}
		nt := c.nt()
		if n := c.countOutbound("Page.getNavigationHistory"); n != 1 {
			t.Fatalf("clean frame retry must not re-read history; got %d", n)
		}
		if nt == nil || !nt.done || !nt.historyLocked || nt.setupProtectedSeen {
			t.Fatalf("locked tab with clean frame retry must settle admitted: %+v", nt)
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("locked-unknown-frame-bound-settles", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = ""
		c.origin = ""
		scheduled := 0
		var deferred *navTarget
		c.g.scheduleInitial = func(nt *navTarget) {
			scheduled++
			deferred = nt
		}
		hsGuardAttach(t, c)
		for c.nt() != nil && !c.nt().done && deferred != nil && scheduled < 8 {
			if msg := c.g.retryInitial(deferred); msg != nil {
				c.outbound([][]byte{msg})
			} else {
				break
			}
		}
		nt := c.nt()
		if n := c.countOutbound("Page.getNavigationHistory"); n != 1 {
			t.Fatalf("bound settle must not re-read history; got %d getNavigationHistory", n)
		}
		if scheduled != maxInitialUnknowns-1 {
			t.Fatalf("want %d scheduleInitial calls before bound settle; scheduled=%d", maxInitialUnknowns-1, scheduled)
		}
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("want settled history-locked tab after unknown bound: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("protected-url-error-page-settles", func(t *testing.T) {
		c := hsNewChrome(t)
		c.protectedEntry()
		c.url = hsChromeErr
		c.origin = "://"
		c.unreach = hsMissing
		scheduled := c.countScheduleAndSettle(t)
		hsGuardAttach(t, c)
		nt := c.nt()
		if *scheduled != 0 {
			t.Fatalf("protected-URL error page must settle without defer; scheduled=%d", *scheduled)
		}
		if nt == nil || !nt.done {
			t.Fatalf("want settled after protected error page: %+v", nt)
		}
		c.requireLatched()
	})
	t.Run("historyProtected-second-callback-no-latch", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		c.history = []map[string]any{
			{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
		}
		c.current = 0
		c.historyAlt = []map[string]any{
			{"id": 1, "url": hsSeven, "userTypedURL": hsSeven, "title": "seven", "transitionType": "typed"},
			{"id": 2, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "link"},
		}
		c.currentAlt = 1
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("want historyLocked after second history callback: %+v", nt)
		}
		if nt.setupProtectedSeen {
			t.Fatal("second-callback historyProtected must not latch after a clean frame")
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("second-callback-unknown-locks", func(t *testing.T) {
		c := hsNewChrome(t)
		c.url = ""
		c.origin = ""
		c.frameURL = hsOrdinary
		c.frameOrig = "https://ordinary.test"
		c.history = []map[string]any{
			{"id": 1, "url": hsOrdinary, "userTypedURL": hsOrdinary, "title": "page", "transitionType": "typed"},
		}
		c.current = 0
		c.historyAlt = []map[string]any{}
		c.currentAlt = 0
		c.attach()
		nt := c.nt()
		if nt == nil || !nt.done || !nt.historyLocked {
			t.Fatalf("second-callback unknown must settle historyLocked as on main: %+v", nt)
		}
		c.requireLatched()
		c.requireRefused(hsSetup[0], hsTextHistory)
	})
}

func TestHistorySetupDocumentsOnlyLockLatches(t *testing.T) {
	c := hsLockedTab(t)
	c.nt().documentsOnly = true
	c.g.mu.Lock()
	c.g.lockFramesLocked(c.nt())
	c.g.mu.Unlock()
	c.requireLatched()
	c.requireRefused(hsSetup[0], hsTextHistory)
}

func TestHistorySetupPendingNavConfirmDoesNotClearLatch(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Fetch.requestPaused", map[string]any{
		"requestId": "F-L9", "networkId": "L9", "frameId": hsTarget, "resourceType": "Document",
		"request": map[string]any{"url": hsSeven, "method": "GET", "headers": map[string]any{}},
	}))
	c.requireLatched()
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L-ord", "url": hsOrdinary, "navigationType": "differentDocument",
	}))
	if len(c.nt().loading) == 0 && !c.nt().unsure {
		t.Fatalf("want pending main-frame load: loading=%v unsure=%v", c.nt().loading, c.nt().unsure)
	}
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	c.url, c.origin, c.loader, c.unreach = hsOrdinary, "https://ordinary.test", "L-still", ""
	c.answerHeld(false)
	if !c.nt().setupProtectedSeen {
		t.Fatal("latch must stay while a main-frame load is still pending")
	}
	c.requireRefused(hsSetup[0], hsTextHistory)
}

func TestHistorySetupLatchClearedByOrdinaryConfirm(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
	}))
	c.requireLatched()
	seen := c.nt().setupProtectedSeq
	c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	issued := int64(0)
	if c.nt().confirm != nil {
		issued = c.nt().confirm.issuedSeq
	}
	if issued <= seen {
		t.Fatalf("child-hold confirm issuedSeq=%d not after sighting seq=%d", issued, seen)
	}
	c.url, c.origin, c.loader, c.unreach = hsOrdinary, "https://ordinary.test", "L-clear", ""
	c.answerHeld(false)
	if c.nt().setupProtectedSeen {
		t.Fatalf("latch not cleared after child-hold ordinary confirm issuedSeq=%d seenSeq=%d", issued, seen)
	}
	c.requireForwarded(hsSetup[0])
	c.requireForwarded(hsSetup[1])
}

func TestHistorySetupLatchClearedByBFCacheRestore(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
	}))
	c.requireLatched()
	seen := c.nt().setupProtectedSeq
	c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
	c.clientRestore(hsOrdinary, "https://ordinary.test", "L-bf")
	issued := int64(0)
	if c.nt().confirm != nil {
		issued = c.nt().confirm.issuedSeq
	}
	if issued <= seen {
		t.Fatalf("BFCache confirm issuedSeq=%d not after sighting seq=%d", issued, seen)
	}
	c.answerHeld(false)
	if c.nt().setupProtectedSeen {
		t.Fatalf("latch not cleared after BFCache ordinary restore issuedSeq=%d seenSeq=%d", issued, seen)
	}
	c.requireForwarded(hsSetup[0])
	c.requireForwarded(hsSetup[1])
}

func TestHistorySetupProtectedNavURLDoesNotClearLatch(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L-nav", "url": hsSeven, "navigationType": "historyDifferentDocument",
	}))
	c.requireLatched()
	if c.nt().navigationURL != hsSeven {
		t.Fatalf("want protected navigationURL from frameStartedNavigating: %q", c.nt().navigationURL)
	}
	c.feed(hsEvent(hsNav, "Network.loadingFailed", map[string]any{
		"requestId": "L-nav", "type": "Document", "errorText": "net::ERR_ABORTED", "canceled": true,
	}))
	if len(c.nt().loading) != 0 || c.nt().unsure {
		t.Fatalf("want empty loading after loadingFailed: loading=%v unsure=%v", c.nt().loading, c.nt().unsure)
	}
	if c.nt().navigationURL != hsSeven {
		t.Fatalf("navigationURL must remain after loadingFailed: %q", c.nt().navigationURL)
	}
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	c.url, c.origin, c.loader, c.unreach = hsOrdinary, "https://ordinary.test", "L-ord", ""
	c.answerHeld(false)
	if !c.nt().setupProtectedSeen {
		t.Fatal("latch must stay while navigationURL is still protected")
	}
	c.requireRefused(hsSetup[0], hsTextHistory)
}

func TestHistorySetupClearSites(t *testing.T) {
	t.Run("final-branch", func(t *testing.T) {
		c := hsLockedTab(t)
		c.hold = true
		c.requireForwarded(hsSetup[1])
		c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
			"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
		}))
		c.requireLatched()
		c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
		c.client = nil
		c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
		c.kidAttached("NEW")
		c.url, c.origin, c.loader = hsOrdinary, "https://ordinary.test", "L-final"
		c.answerHeld(false)
		if c.nt().setupProtectedSeen {
			t.Fatal("final-branch clear must drop the latch")
		}
		c.requireForwarded(hsSetup[0])
	})
	t.Run("inherited-branch", func(t *testing.T) {
		c := hsLockedTab(t)
		c.hold = true
		c.requireForwarded(hsSetup[1])
		c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
			"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
		}))
		c.requireLatched()
		c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
		c.client = nil
		c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
		c.kidAttached("NEW")
		c.url, c.origin, c.loader, c.unreach = "about:blank", "https://ordinary.test", "L-inh", ""
		c.answerHeld(false)
		if c.nt().setupProtectedSeen {
			t.Fatal("inherited-branch clear must drop the latch when origin is known")
		}
		c.requireForwarded(hsSetup[0])
	})
}

func TestHistorySetupConfirmTopTargetURLLatch(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
	}))
	c.requireLatched()
	c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
	c.g.targets[hsTarget].url = hsSeven
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	c.url, c.origin, c.loader = hsOrdinary, "https://ordinary.test", "L-top"
	c.answerHeld(false)
	c.requireLatched()
	c.requireRefused(hsSetup[0], hsTextHistory)
}

func TestHistorySetupInheritedEmptyOriginKeepsLatch(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.requireForwarded(hsSetup[1])
	c.feed(hsEvent(hsNav, "Page.frameStartedNavigating", map[string]any{
		"frameId": hsTarget, "loaderId": "L9", "url": hsSeven, "navigationType": "historyDifferentDocument",
	}))
	c.requireLatched()
	c.feed(hsEvent(hsNav, "Page.frameStoppedLoading", map[string]any{"frameId": hsTarget}))
	c.client = nil
	c.kidAnnounced("NEW", "iframe", "https://third.test/embed", true)
	c.kidAttached("NEW")
	c.url, c.origin, c.loader, c.unreach = "about:blank", "", "L-empty-o", ""
	c.answerHeld(false)
	c.requireLatched()
	c.requireRefused(hsSetup[0], hsTextHistory)
}

func TestHistorySetupStaleConfirmDoesNotClearLatch(t *testing.T) {
	c := hsLockedTab(t)
	c.hold = true
	c.g.mu.Lock()
	c.g.beginConfirmLocked(c.nt())
	send := c.g.confirmTargetLocked(c.nt())
	c.g.mu.Unlock()
	c.outbound(send)
	staleIssued := c.nt().confirm.issuedSeq
	c.g.mu.Lock()
	c.nt().setupProtectedSeen = true
	c.nt().setupProtectedSeq = staleIssued
	c.g.mu.Unlock()
	c.url, c.origin, c.loader, c.unreach = hsOrdinary, "https://ordinary.test", "L-stale", ""
	c.answerHeld(false)
	if !c.nt().setupProtectedSeen {
		t.Fatalf("confirm issued at sighting (issuedSeq=%d) must not clear the latch", staleIssued)
	}
	c.requireRefused(hsSetup[0], hsTextHistory)
}
