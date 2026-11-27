package cdp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	holdTarget   = "HOLD-TARGET"
	holdSession  = "S-HOLD"
	holdNav      = "S-HOLD-NAV"
	holdAttachID = 80
	holdURL      = "https://ordinary.test/doc"
	holdVault    = "https://vault.sliccy.ai/doc"
	holdMarker   = "HOLD-HELD-BYTES"
)

type holdCall struct {
	id      int64
	method  string
	session string
	done    bool
}

type holdRig struct {
	t       *testing.T
	g       *guard
	mu      sync.Mutex
	logs    []string
	pages   []string
	calls   []*holdCall
	flushed chan [][]byte
}

func holdNewRig(t *testing.T) *holdRig {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	r := &holdRig{t: t, flushed: make(chan [][]byte, 64)}
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
	r.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": holdTarget, "type": "page", "url": holdURL, "browserContextId": "CTX"}}}))
	r.g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: holdTarget, session: holdNav, ready: ready, done: true, fetchArmed: true, documentURL: holdURL, loaderID: "L1"}
	r.g.navTargets[holdTarget] = nt
	r.g.navSessions[holdNav] = nt
	r.g.mu.Unlock()
	r.calls = nil
	pv := r.g.fromCommand(&command{id: holdAttachID, method: "Target.attachToTarget", params: map[string]any{"targetId": holdTarget, "flatten": true}}, holdAttachID, true)
	if pv.reply != nil {
		t.Fatalf("attach refused: %s", pv.reply)
	}
	r.sent(pv.send)
	r.drain()
	r.browser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": holdSession, "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": holdTarget, "type": "page", "url": holdURL, "browserContextId": "CTX"}}}))
	r.drain()
	r.browser(encode(map[string]any{"id": holdAttachID, "result": map[string]any{"sessionId": holdSession}}))
	r.drain()
	r.g.mu.Lock()
	passed := r.g.docPassed[holdSession]
	r.g.mu.Unlock()
	if !passed {
		t.Fatalf("client session not admitted: pages=%v", r.pages)
	}
	r.calls = nil
	r.pages = nil
	return r
}

func (r *holdRig) drain() {
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
			r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"targetInfo": map[string]any{"targetId": holdTarget, "type": "page", "url": holdURL, "browserContextId": "CTX", "attached": true}}}))
			continue
		}
		r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": holdTarget, "loaderId": "L-SETUP", "url": holdURL, "securityOrigin": "https://ordinary.test", "mimeType": "text/html"}}}}))
	}
}

func (r *holdRig) take(v browserVerdict) {
	if v.forward != nil {
		r.pages = append(r.pages, string(v.forward))
	}
	for _, p := range v.page {
		r.pages = append(r.pages, string(p))
	}
	r.sent(v.send)
}

func (r *holdRig) sent(send [][]byte) {
	for _, raw := range send {
		var m struct {
			ID        *int64 `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &m) != nil || m.ID == nil {
			continue
		}
		r.calls = append(r.calls, &holdCall{id: *m.ID, method: m.Method, session: m.SessionID})
	}
}

func (r *holdRig) browser(raw []byte) {
	r.take(r.g.fromBrowser(raw))
}

func (r *holdRig) read(id int64, method string, params map[string]any) {
	r.t.Helper()
	pv := r.g.fromCommand(&command{id: id, method: method, session: holdSession, params: params}, id, true)
	if pv.forward == nil {
		r.t.Fatalf("%s not forwarded: reply=%s", method, pv.reply)
	}
	r.sent(pv.send)
}

func (r *holdRig) reply(id int64, method string) {
	result := map[string]any{"result": map[string]any{"type": "string", "value": holdMarker}}
	r.browser(encode(map[string]any{"id": id, "sessionId": holdSession, "result": result}))
}

func (r *holdRig) count(method, session string) int {
	n := 0
	for _, c := range r.calls {
		if c.method == method && (session == "" || c.session == session) {
			n++
		}
	}
	return n
}

func (r *holdRig) next(method string) *holdCall {
	for _, c := range r.calls {
		if c.method == method && !c.done {
			return c
		}
	}
	return nil
}

func (r *holdRig) answerInfo(url string) {
	r.t.Helper()
	c := r.next("Target.getTargetInfo")
	if c == nil {
		r.t.Fatalf("no Target.getTargetInfo to answer: calls=%d", len(r.calls))
	}
	c.done = true
	r.browser(encode(map[string]any{"id": c.id, "sessionId": c.session, "result": map[string]any{"targetInfo": map[string]any{"targetId": holdTarget, "type": "page", "url": url, "browserContextId": "CTX", "attached": true}}}))
}

func (r *holdRig) outcome(id int64) string {
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
		case strings.Contains(p, holdMarker):
			return "delivered"
		default:
			return "other:" + p
		}
	}
	return "none"
}

func (r *holdRig) cut() bool {
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	s := r.g.sessions[holdSession]
	if s == nil || s.refused || r.g.refused[holdSession] {
		return true
	}
	return false
}

func (r *holdRig) detached() bool {
	return r.count("Target.detachFromTarget", "") > 0
}

func (r *holdRig) settledClean() {
	r.t.Helper()
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	if n := len(r.g.docIdentityHolds); n != 0 {
		r.t.Fatalf("holds left after release: %d", n)
	}
	if ask := r.g.docIdentityAsks[holdTarget]; ask != nil {
		r.t.Fatalf("ask left after release: %+v", ask)
	}
}

func (r *holdRig) wantDelivered(id int64) {
	r.t.Helper()
	if got := r.outcome(id); got != "delivered" {
		r.t.Fatalf("outcome %s, want delivered; pages=%v", got, r.pages)
	}
	if r.cut() || r.detached() {
		r.t.Fatalf("the session was cut on a delivered reply")
	}
	r.settledClean()
}

func (r *holdRig) noNavCommand() {
	r.t.Helper()
	if n := r.count("Page.getFrameTree", ""); n != 0 {
		r.t.Fatalf("Page.getFrameTree sent %d times while the hold was open", n)
	}
}

func TestDocIdentityReleaseAtTargetInfo(t *testing.T) {
	t.Run("reply-released-at-target-info-before-later-taint", func(t *testing.T) {
		r := holdNewRig(t)
		r.g.mu.Lock()
		r.g.navTargets[holdTarget].fetchArmed = true
		r.g.mu.Unlock()
		r.read(100, "Runtime.evaluate", map[string]any{"expression": `location.href="` + holdVault + `"`})
		r.reply(100, "Runtime.evaluate")
		r.noNavCommand()
		r.answerInfo(holdURL)
		r.noNavCommand()
		r.wantDelivered(100)
		r.browser(encode(map[string]any{"sessionId": holdNav, "method": "Page.frameStartedNavigating", "params": map[string]any{"frameId": holdTarget, "url": holdVault, "loaderId": "L2", "navigationType": "differentDocument"}}))
		r.g.mu.Lock()
		r.g.bumpDocIdentityTaintLocked(holdTarget)
		r.g.mu.Unlock()
		if r.cut() || r.detached() {
			t.Fatalf("the session was cut after the delivered reply")
		}
		r.noNavCommand()
	})
}
