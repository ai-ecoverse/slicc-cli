package cdp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	dlTarget   = "DL-TARGET"
	dlSession  = "S-DL"
	dlNav      = "S-DL-NAV"
	dlAttachID = 80
	dlURL      = "https://ordinary.test/doc"
	dlVault    = "https://vault.sliccy.ai/doc"
	dlMarker   = "DL-HELD-BYTES"
	dlDropLog  = "a held reply's document was replaced"
)

type dlCall struct {
	id      int64
	method  string
	session string
	done    bool
}

type dlRig struct {
	t       *testing.T
	g       *guard
	mu      sync.Mutex
	logs    []string
	pages   []string
	calls   []*dlCall
	flushed chan [][]byte
}

func dlMethods() []struct {
	method string
	params map[string]any
} {
	return []struct {
		method string
		params map[string]any
	}{
		{"Runtime.evaluate", map[string]any{"expression": "document.title"}},
		{"DOM.getDocument", map[string]any{}},
		{"DOM.getOuterHTML", map[string]any{"nodeId": json.Number("1")}},
		{"DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []any{}}},
		{"Page.captureScreenshot", map[string]any{}},
		{"Page.printToPDF", map[string]any{}},
		{"Page.getResourceContent", map[string]any{"frameId": dlTarget, "url": dlURL}},
		{"Accessibility.getFullAXTree", map[string]any{}},
		{"Accessibility.getPartialAXTree", map[string]any{"nodeId": json.Number("1")}},
		{"Accessibility.queryAXTree", map[string]any{"nodeId": json.Number("1")}},
	}
}

func dlReplyFor(method string, id int64) []byte {
	var result map[string]any
	switch method {
	case "Runtime.evaluate":
		result = map[string]any{"result": map[string]any{"type": "string", "value": dlMarker}}
	case "DOM.getDocument":
		result = map[string]any{"root": map[string]any{"nodeId": 1, "nodeName": "#document", "documentURL": dlURL, "baseURL": dlURL, "children": []any{map[string]any{"nodeId": 2, "nodeName": "#text", "nodeValue": dlMarker}}}}
	case "DOM.getOuterHTML":
		result = map[string]any{"outerHTML": "<html>" + dlMarker + "</html>"}
	case "DOMSnapshot.captureSnapshot":
		result = map[string]any{"documents": []any{}, "strings": []any{dlMarker}}
	case "Page.captureScreenshot", "Page.printToPDF":
		result = map[string]any{"data": dlMarker}
	case "Page.getResourceContent":
		result = map[string]any{"content": dlMarker, "base64Encoded": false}
	default:
		result = map[string]any{"nodes": []any{map[string]any{"nodeId": "1", "name": map[string]any{"type": "computedString", "value": dlMarker}}}}
	}
	return encode(map[string]any{"id": id, "sessionId": dlSession, "result": result})
}

func dlNewRig(t *testing.T, loader string, nav bool) *dlRig {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	r := &dlRig{t: t, flushed: make(chan [][]byte, 64)}
	r.g = newGuard(o, func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		r.mu.Lock()
		r.logs = append(r.logs, line)
		r.mu.Unlock()
	})
	r.g.relaySession = "RELAY"
	r.g.flushAsync = func(send, page [][]byte) {
		r.flushed <- page
	}
	r.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": dlTarget, "type": "page", "url": dlURL, "browserContextId": "CTX"}}}))
	r.g.mu.Lock()
	if nav {
		ready := make(chan struct{})
		close(ready)
		nt := &navTarget{target: dlTarget, session: dlNav, ready: ready, done: true, fetchArmed: true, documentURL: dlURL, loaderID: loader}
		r.g.navTargets[dlTarget] = nt
		r.g.navSessions[dlNav] = nt
	}
	r.g.mu.Unlock()
	r.calls = nil
	pv := r.g.fromCommand(&command{id: dlAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": dlTarget, "flatten": true}}, dlAttachID, true)
	if pv.reply != nil {
		t.Fatalf("attach refused: %s", pv.reply)
	}
	r.sent(pv.send)
	r.drain()
	r.browser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": dlSession, "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": dlTarget, "type": "page", "url": dlURL, "browserContextId": "CTX"}}}))
	r.drain()
	r.browser(encode(map[string]any{"id": dlAttachID, "result": map[string]any{"sessionId": dlSession}}))
	r.drain()
	r.g.mu.Lock()
	passed := r.g.docPassed[dlSession]
	r.g.mu.Unlock()
	if !passed {
		t.Fatalf("client session not admitted: pages=%v", r.pages)
	}
	r.calls = nil
	r.pages = nil
	return r
}

func (r *dlRig) drain() {
	for {
		c := r.next("Target.getTargetInfo")
		if c == nil {
			c = r.next("Page.getFrameTree")
		}
		if c == nil {
			return
		}
		c.done = true
		if c.method == "Target.getTargetInfo" {
			r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"targetInfo": map[string]any{"targetId": dlTarget, "type": "page", "url": dlURL, "browserContextId": "CTX", "attached": true}}}))
			continue
		}
		r.answerTreeCall(c, dlTarget, "L-SETUP")
	}
}

func (r *dlRig) take(v browserVerdict) {
	if v.forward != nil {
		r.pages = append(r.pages, string(v.forward))
	}
	for _, p := range v.page {
		r.pages = append(r.pages, string(p))
	}
	r.sent(v.send)
}

func (r *dlRig) sent(send [][]byte) {
	for _, raw := range send {
		var m struct {
			ID        *int64 `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &m) != nil || m.ID == nil {
			continue
		}
		r.calls = append(r.calls, &dlCall{id: *m.ID, method: m.Method, session: m.SessionID})
	}
}

func (r *dlRig) browser(raw []byte) {
	r.take(r.g.fromBrowser(raw))
}

func (r *dlRig) read(id int64, method string, params map[string]any) {
	r.t.Helper()
	pv := r.g.fromCommand(&command{id: id, method: method, session: dlSession, params: params}, id, true)
	if pv.forward == nil {
		r.t.Fatalf("%s not forwarded: reply=%s", method, pv.reply)
	}
	r.sent(pv.send)
}

func (r *dlRig) reply(id int64, method string) {
	r.browser(dlReplyFor(method, id))
}

func (r *dlRig) count(method, session string) int {
	n := 0
	for _, c := range r.calls {
		if c.method == method && (session == "" || c.session == session) {
			n++
		}
	}
	return n
}

func (r *dlRig) next(method string) *dlCall {
	for _, c := range r.calls {
		if c.method == method && !c.done {
			return c
		}
	}
	return nil
}

func (r *dlRig) answerInfo(url string) {
	r.t.Helper()
	c := r.next("Target.getTargetInfo")
	if c == nil {
		r.t.Fatalf("no Target.getTargetInfo to answer: calls=%d", len(r.calls))
	}
	c.done = true
	r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"targetInfo": map[string]any{"targetId": dlTarget, "type": "page", "url": url, "browserContextId": "CTX", "attached": true}}}))
}

func (r *dlRig) treeCall() *dlCall {
	r.t.Helper()
	c := r.next("Page.getFrameTree")
	if c == nil {
		r.t.Fatalf("no Page.getFrameTree to answer: calls=%d", len(r.calls))
	}
	c.done = true
	return c
}

func (r *dlRig) answerTreeCall(c *dlCall, frame, loader string) {
	r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": frame, "loaderId": loader, "url": dlURL, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}}))
}

func (r *dlRig) answerTree(loader string) {
	r.answerTreeCall(r.treeCall(), dlTarget, loader)
}

func (r *dlRig) answerTreeError() {
	c := r.treeCall()
	r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "error": map[string]any{"code": -32000, "message": "Not attached to an active page"}}))
}

func (r *dlRig) commit(url, loader string) {
	r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.frameNavigated", "params": map[string]any{"type": "Navigation", "frame": map[string]any{"id": dlTarget, "loaderId": loader, "url": url, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}))
}

func (r *dlRig) sameDocument(url string) {
	r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.navigatedWithinDocument", "params": map[string]any{"frameId": dlTarget, "url": url, "navigationType": "fragment"}}))
}

func (r *dlRig) setTracker(loader string) {
	r.g.mu.Lock()
	r.g.navTargets[dlTarget].loaderID = loader
	r.g.mu.Unlock()
}

func (r *dlRig) outcome(id int64) string {
	key := `"id":` + strconv.FormatInt(id, 10) + `,`
	for _, p := range r.pages {
		if !strings.Contains(p, key) {
			continue
		}
		switch {
		case strings.Contains(p, refusedDocRetriable):
			return "retriable"
		case strings.Contains(p, "slicc: refused"):
			return "refused"
		case strings.Contains(p, dlMarker):
			return "delivered"
		default:
			return "other:" + p
		}
	}
	return "none"
}

func (r *dlRig) waitOutcome(id int64, within time.Duration) (string, time.Duration) {
	start := time.Now()
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	for {
		if o := r.outcome(id); o != "none" {
			return o, time.Since(start)
		}
		select {
		case page := <-r.flushed:
			for _, p := range page {
				r.pages = append(r.pages, string(p))
			}
		case <-deadline.C:
			return r.outcome(id), time.Since(start)
		}
	}
}

func (r *dlRig) cut() bool {
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	s := r.g.sessions[dlSession]
	if s == nil || s.refused || r.g.refused[dlSession] {
		return true
	}
	return false
}

func (r *dlRig) detached() bool {
	return r.count("Target.detachFromTarget", "") > 0
}

func (r *dlRig) logged(sub string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func (r *dlRig) settledClean() {
	r.t.Helper()
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	if n := len(r.g.docIdentityHolds); n != 0 {
		r.t.Fatalf("holds left after release: %d", n)
	}
	if ask := r.g.docIdentityAsks[dlTarget]; ask != nil {
		r.t.Fatalf("ask left after release: %+v", ask)
	}
}

func (r *dlRig) wantDropNoCut(id int64) {
	r.t.Helper()
	if got := r.outcome(id); got != "retriable" {
		r.t.Fatalf("outcome %s, want the retriable drop; pages=%v", got, r.pages)
	}
	if r.cut() || r.detached() {
		r.t.Fatalf("the session was cut on a replaced document")
	}
	r.settledClean()
}

func (r *dlRig) wantDelivered(id int64) {
	r.t.Helper()
	if got := r.outcome(id); got != "delivered" {
		r.t.Fatalf("outcome %s, want delivered; pages=%v", got, r.pages)
	}
	if r.cut() || r.detached() {
		r.t.Fatalf("the session was cut on a delivered reply")
	}
	if l := r.logged(dlDropLog); l != "" {
		r.t.Fatalf("delivered reply logged a drop: %s", l)
	}
	r.settledClean()
}

func (r *dlRig) wantCut(id int64) {
	r.t.Helper()
	if got := r.outcome(id); got != "refused" {
		r.t.Fatalf("outcome %s, want the existing refusal; pages=%v", got, r.pages)
	}
	if !r.cut() || !r.detached() {
		r.t.Fatalf("the existing case must keep its session cut (cut=%t detached=%t)", r.cut(), r.detached())
	}
	if l := r.logged(dlDropLog); l != "" {
		r.t.Fatalf("an existing case was decided by the loader: %s", l)
	}
	r.settledClean()
}

func (r *dlRig) noTreeAsk() {
	r.t.Helper()
	if n := r.count("Page.getFrameTree", ""); n != 0 {
		r.t.Fatalf("Page.getFrameTree sent %d times; the hold judges by the tracker alone", n)
	}
}

func TestDocLoaderMethods(t *testing.T) {
	for _, m := range dlMethods() {
		t.Run(m.method, func(t *testing.T) {
			t.Run("replaced", func(t *testing.T) {
				r := dlNewRig(t, "L1", true)
				r.read(100, m.method, m.params)
				r.commit(dlURL, "L2")
				r.reply(100, m.method)
				if r.count("Target.getTargetInfo", "RELAY") != 1 {
					t.Fatalf("the hold must ask Target.getTargetInfo once")
				}
				r.noTreeAsk()
				r.answerInfo(dlURL)
				r.wantDropNoCut(100)
				if l := r.logged(dlDropLog); !strings.Contains(l, "admitted=L1 current=L2") {
					t.Fatalf("drop log %q", l)
				}
				r.noTreeAsk()
			})
			t.Run("same-loader", func(t *testing.T) {
				r := dlNewRig(t, "L1", true)
				r.read(100, m.method, m.params)
				r.reply(100, m.method)
				r.answerInfo(dlURL)
				r.wantDelivered(100)
				r.noTreeAsk()
			})
		})
	}
}

func TestDocLoaderSources(t *testing.T) {
	ev := dlMethods()[0]
	t.Run("same-document", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.sameDocument(dlURL + "#section")
		r.reply(100, ev.method)
		r.answerInfo(dlURL + "#section")
		r.wantDelivered(100)
		r.noTreeAsk()
	})
	t.Run("iframe-only", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.frameNavigated", "params": map[string]any{"type": "Navigation", "frame": map[string]any{"id": "CHILD-FRAME", "parentId": dlTarget, "loaderId": "L-CHILD", "url": "https://ordinary.test/child", "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}))
		r.reply(100, ev.method)
		r.answerInfo(dlURL)
		r.wantDelivered(100)
		r.noTreeAsk()
	})
	t.Run("tracker-replaced", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.commit(dlURL, "L2")
		r.reply(100, ev.method)
		r.answerInfo(dlURL)
		r.wantDropNoCut(100)
		if l := r.logged(dlDropLog); !strings.Contains(l, "admitted=L1 current=L2") {
			t.Fatalf("drop log %q", l)
		}
		r.noTreeAsk()
	})
	t.Run("bfcache-same-loader", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.frameNavigated", "params": map[string]any{"type": "BackForwardCacheRestore", "frame": map[string]any{"id": dlTarget, "loaderId": "L1", "url": dlURL, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}))
		r.reply(100, ev.method)
		r.answerInfo(dlURL)
		r.wantDelivered(100)
	})
	t.Run("admission-unknown", func(t *testing.T) {
		r := dlNewRig(t, "", true)
		r.read(100, ev.method, ev.params)
		r.commit(dlURL, "L2")
		r.reply(100, ev.method)
		r.answerInfo(dlURL)
		r.wantDelivered(100)
		r.noTreeAsk()
	})
	t.Run("current-unknown", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.reply(100, ev.method)
		r.setTracker("")
		r.answerInfo(dlURL)
		r.wantDelivered(100)
		r.noTreeAsk()
	})
	t.Run("href-info-before-taint", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.g.mu.Lock()
		r.g.navTargets[dlTarget].fetchArmed = true
		r.g.mu.Unlock()
		start := time.Now()
		var timeline []string
		mark := func(label string) {
			timeline = append(timeline, fmt.Sprintf("%8s %-28s outcome=%s getTargetInfo=%d getFrameTree=%d cut=%t", time.Since(start).Round(time.Microsecond), label, r.outcome(100), r.count("Target.getTargetInfo", "RELAY"), r.count("Page.getFrameTree", ""), r.cut()))
		}
		defer func() {
			t.Logf("timeline:\n%s", strings.Join(timeline, "\n"))
		}()
		r.read(100, "Runtime.evaluate", map[string]any{"expression": `location.href="` + dlVault + `"`})
		mark("read forwarded")
		r.reply(100, "Runtime.evaluate")
		mark("reply held")
		r.noTreeAsk()
		r.answerInfo(dlURL)
		mark("info answered")
		r.noTreeAsk()
		r.wantDelivered(100)
		r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.frameStartedNavigating", "params": map[string]any{"frameId": dlTarget, "url": dlVault, "loaderId": "L2", "navigationType": "differentDocument"}}))
		mark("navigation started")
		r.g.mu.Lock()
		r.g.bumpDocIdentityTaintLocked(dlTarget)
		r.g.mu.Unlock()
		mark("taint bumped")
		if r.cut() || r.detached() {
			t.Fatalf("the session was cut after the delivered reply")
		}
		r.noTreeAsk()
	})
}

func TestDocLoaderPrecedence(t *testing.T) {
	ev := dlMethods()[0]
	t.Run("protected-post-read", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.commit(dlURL, "L2")
		r.reply(100, ev.method)
		r.answerInfo(dlVault)
		r.wantCut(100)
	})
	t.Run("tainted", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.g.mu.Lock()
		r.g.bumpDocIdentityTaintLocked(dlTarget)
		r.g.mu.Unlock()
		r.commit(dlURL, "L2")
		r.reply(100, ev.method)
		r.answerInfo(dlURL)
		r.wantCut(100)
	})
	t.Run("chrome-error-after-hold", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.reply(100, ev.method)
		r.browser(encode(map[string]any{"sessionId": dlNav, "method": "Page.frameNavigated", "params": map[string]any{"type": "Navigation", "frame": map[string]any{"id": dlTarget, "loaderId": "L-ERR", "url": "chrome-error://chromewebdata/", "unreachableUrl": dlURL, "securityOrigin": "://", "mimeType": "text/html"}}}))
		r.answerInfo("chrome-error://chromewebdata/")
		if got := r.outcome(100); got != "delivered" {
			t.Fatalf("the chrome-error case keeps precedence (a reply held before the error page commits is delivered), got %s pages=%v", got, r.pages)
		}
		if l := r.logged(dlDropLog); l != "" {
			t.Fatalf("the chrome-error case was decided by the loader: %s", l)
		}
	})
	t.Run("session-refused", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.commit(dlURL, "L2")
		r.reply(100, ev.method)
		r.g.mu.Lock()
		r.g.addRefusedLocked(dlSession)
		r.g.mu.Unlock()
		r.answerInfo(dlURL)
		if got := r.outcome(100); got != "refused" {
			t.Fatalf("outcome %s, want the refused-session answer; pages=%v", got, r.pages)
		}
		if l := r.logged(dlDropLog); l != "" {
			t.Fatalf("a refused session was decided by the loader: %s", l)
		}
	})
}

func TestDocLoaderHolds(t *testing.T) {
	ev := dlMethods()[0]
	t.Run("different-admission-loaders", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.commit(dlURL, "L2")
		r.read(101, ev.method, ev.params)
		r.reply(100, ev.method)
		time.Sleep(2 * time.Millisecond)
		r.reply(101, ev.method)
		if n := r.count("Target.getTargetInfo", "RELAY"); n != 1 {
			t.Fatalf("one ask in flight must send one Target.getTargetInfo, got %d", n)
		}
		r.noTreeAsk()
		r.answerInfo(dlURL)
		if got := r.outcome(100); got != "retriable" {
			t.Fatalf("the hold admitted on L1 must be dropped: %s pages=%v", got, r.pages)
		}
		if got := r.outcome(101); got != "none" {
			t.Fatalf("the hold that arrived after the ask started must be re-asked, got %s", got)
		}
		if r.count("Target.getTargetInfo", "RELAY") != 2 {
			t.Fatalf("the re-ask must send one more Target.getTargetInfo")
		}
		r.noTreeAsk()
		r.answerInfo(dlURL)
		if got := r.outcome(101); got != "delivered" {
			t.Fatalf("the hold admitted on L2 must be delivered: %s pages=%v", got, r.pages)
		}
		if r.cut() || r.detached() {
			t.Fatalf("no session cut expected")
		}
		r.settledClean()
	})
}

func TestDocLoaderLedger(t *testing.T) {
	ev := dlMethods()[0]
	t.Run("no-tree-ask", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		r.read(100, ev.method, ev.params)
		r.reply(100, ev.method)
		if r.count("Target.getTargetInfo", "RELAY") != 1 {
			t.Fatalf("exactly one Target.getTargetInfo on the relay session, calls=%d", len(r.calls))
		}
		r.noTreeAsk()
		r.answerInfo(dlURL)
		r.wantDelivered(100)
		r.noTreeAsk()
		if n := r.count("Target.getTargetInfo", ""); n != 1 {
			t.Fatalf("Target.getTargetInfo count %d after release", n)
		}
	})
	t.Run("several-holds", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		for id := int64(100); id < 104; id++ {
			r.read(id, ev.method, ev.params)
		}
		for id := int64(100); id < 104; id++ {
			r.reply(id, ev.method)
		}
		if n := r.count("Target.getTargetInfo", "RELAY"); n != 1 {
			t.Fatalf("holds sharing an ask in flight sent %d Target.getTargetInfo", n)
		}
		r.g.mu.Lock()
		at := r.g.docIdentityAsks[dlTarget].started
		for _, h := range r.g.docIdentityHolds {
			h.arrived = at
		}
		r.g.mu.Unlock()
		r.answerInfo(dlURL)
		for id := int64(100); id < 104; id++ {
			if got := r.outcome(id); got != "delivered" {
				t.Fatalf("hold %d: %s", id, got)
			}
		}
		if infos := r.count("Target.getTargetInfo", "RELAY"); infos != 1 {
			t.Fatalf("one Target.getTargetInfo per ask: infos=%d for 4 holds", infos)
		}
		r.noTreeAsk()
		r.settledClean()
	})
	t.Run("shared-ask", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		methods := dlMethods()[:4]
		for i, m := range methods {
			r.read(int64(100+i), m.method, m.params)
		}
		for i, m := range methods {
			r.reply(int64(100+i), m.method)
		}
		r.g.mu.Lock()
		at := r.g.docIdentityAsks[dlTarget].started
		for _, h := range r.g.docIdentityHolds {
			h.arrived = at
		}
		r.g.mu.Unlock()
		r.answerInfo(dlURL)
		for i := range methods {
			if got := r.outcome(int64(100 + i)); got != "delivered" {
				t.Fatalf("hold %d: %s", 100+i, got)
			}
		}
		if infos := r.count("Target.getTargetInfo", "RELAY"); infos != 1 {
			t.Fatalf("four holds sharing one ask: asks=%d", infos)
		}
		r.noTreeAsk()
		r.settledClean()
	})
	t.Run("no-hold", func(t *testing.T) {
		r := dlNewRig(t, "L1", true)
		pv := r.g.fromCommand(&command{id: 100, method: "Page.enable", session: dlSession, params: map[string]any{}}, 100, true)
		r.sent(pv.send)
		r.browser(encode(map[string]any{"id": 100, "sessionId": dlSession, "result": map[string]any{}}))
		r.browser(encode(map[string]any{"sessionId": dlSession, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 7, "origin": "https://ordinary.test", "name": "", "uniqueId": "U-7", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": dlTarget}}}}))
		pv = r.g.fromCommand(&command{id: 101, method: "Runtime.evaluate", session: dlSession, params: map[string]any{"expression": "1", "contextId": json.Number("7")}}, 101, true)
		if pv.forward == nil {
			t.Fatalf("a pinned read must be forwarded: %s", pv.reply)
		}
		r.sent(pv.send)
		r.browser(encode(map[string]any{"id": 101, "sessionId": dlSession, "result": map[string]any{"result": map[string]any{"type": "number", "value": 1}}}))
		r.noTreeAsk()
		if n := r.count("Target.getTargetInfo", ""); n != 0 {
			t.Fatalf("Target.getTargetInfo without a hold: %d", n)
		}
	})
}
