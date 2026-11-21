package cdp

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	arTarget  = "AR-TARGET"
	arSession = "S-AR"
	arAttach  = 70
)

type arWire struct {
	t      *testing.T
	g      *guard
	client [][]byte
	chrome [][]byte
	asks   []int64
}

func arNew(t *testing.T) *arWire {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	return &arWire{t: t, g: g}
}

func arFields(raw []byte) (id int64, hasID bool, method, session string, params map[string]any) {
	var m struct {
		ID        *int64         `json:"id"`
		Method    string         `json:"method"`
		SessionID string         `json:"sessionId"`
		Params    map[string]any `json:"params"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.ID != nil {
		return *m.ID, true, m.Method, m.SessionID, m.Params
	}
	return 0, false, m.Method, m.SessionID, m.Params
}

func (w *arWire) toChrome(raw []byte) {
	w.chrome = append(w.chrome, raw)
	id, _, method, session, _ := arFields(raw)
	if method == "Target.getTargetInfo" && session == "RELAY" {
		w.asks = append(w.asks, id)
	}
}

func (w *arWire) take(v browserVerdict) {
	if v.forward != nil {
		w.client = append(w.client, v.forward)
	}
	w.client = append(w.client, v.page...)
	for _, raw := range v.send {
		w.toChrome(raw)
	}
}

func (w *arWire) browser(raw []byte) {
	w.take(w.g.fromBrowser(raw))
}

func (w *arWire) command(id int64, session, method string, params map[string]any) pageVerdict {
	raw := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		raw["sessionId"] = session
	}
	c, n, ok := parseCommand(encode(raw))
	if !ok {
		w.t.Fatalf("parse %s", method)
	}
	pv := w.g.fromCommand(c, n, ok)
	if pv.reply != nil {
		w.client = append(w.client, pv.reply)
	}
	if pv.forward != nil {
		w.toChrome(pv.forward)
	}
	for _, raw := range pv.send {
		w.toChrome(raw)
	}
	return pv
}

func (w *arWire) attach(id int64, target string) bool {
	pv := w.command(id, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	return pv.forward != nil
}

func (w *arWire) answerAsks(url string) {
	asks := w.asks
	w.asks = nil
	for _, id := range asks {
		w.browser(arInfoAnswer(id, arTarget, "page", url))
	}
}

func arInfo(target, typ, url string) map[string]any {
	return map[string]any{"targetId": target, "type": typ, "url": url, "browserContextId": "CTX"}
}

func arRelay(method, target, typ, url string) []byte {
	return encode(map[string]any{"sessionId": "RELAY", "method": method, "params": map[string]any{"targetInfo": arInfo(target, typ, url)}})
}

func arRoot(method, target, typ, url string) []byte {
	return encode(map[string]any{"method": method, "params": map[string]any{"targetInfo": arInfo(target, typ, url)}})
}

func arInfoAnswer(id int64, target, typ, url string) []byte {
	return encode(map[string]any{"id": id, "sessionId": "RELAY", "result": map[string]any{"targetInfo": arInfo(target, typ, url)}})
}

func arAttached(parent, session, target, typ, url string, waiting bool) []byte {
	m := map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "waitingForDebugger": waiting, "targetInfo": arInfo(target, typ, url)}}
	if parent != "" {
		m["sessionId"] = parent
	}
	return encode(m)
}

func arReply(id int64, session string) []byte {
	return encode(map[string]any{"id": id, "result": map[string]any{"sessionId": session}})
}

func arDetached(session, target string) []byte {
	return encode(map[string]any{"method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": session, "targetId": target}})
}

const arRefusal = `{"error":{"code":-32000,"message":"slicc: refused: target is on seven's origin"},"id":70}`

func (w *arWire) answersFor(id int64) []string {
	var out []string
	for _, raw := range w.client {
		got, has, _, session, _ := arFields(raw)
		if has && got == id && session == "" {
			out = append(out, string(raw))
		}
	}
	return out
}

func (w *arWire) clientMentions(session string) []string {
	var out []string
	for _, raw := range w.client {
		if strings.Contains(string(raw), session) {
			out = append(out, string(raw))
		}
	}
	return out
}

func (w *arWire) detachesOf(session string) int {
	n := 0
	for _, raw := range w.chrome {
		_, _, method, _, params := arFields(raw)
		if method == "Target.detachFromTarget" && params["sessionId"] == session {
			n++
		}
	}
	return n
}

func (w *arWire) registered(session string) bool {
	w.g.mu.Lock()
	defer w.g.mu.Unlock()
	return w.g.sessions[session] != nil
}

func (w *arWire) mustRefused(id int64, session string) {
	w.t.Helper()
	answers := w.answersFor(id)
	if len(answers) != 1 || !strings.Contains(answers[0], "slicc: refused: target is on seven's origin") {
		w.t.Fatalf("attach %d: want exactly one standard refusal, got %q (client=%q)", id, answers, w.clientStrings())
	}
	if got := w.clientMentions(session); len(got) != 0 {
		w.t.Fatalf("the session Chrome created reached the client: %q", got)
	}
	if w.registered(session) {
		w.t.Fatalf("the session Chrome created was registered as a client session")
	}
	if w.detachesOf(session) < 1 {
		w.t.Fatalf("the session Chrome created was not detached: chrome=%q", w.chromeStrings())
	}
}

func (w *arWire) clientStrings() []string {
	out := make([]string, 0, len(w.client))
	for _, raw := range w.client {
		out = append(out, string(raw))
	}
	return out
}

func (w *arWire) chromeStrings() []string {
	out := make([]string, 0, len(w.chrome))
	for _, raw := range w.chrome {
		out = append(out, string(raw))
	}
	return out
}

func TestAttachReplyCITimeline(t *testing.T) {
	w := arNew(t)
	w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
	if !w.attach(arAttach, arTarget) {
		t.Fatal("attach not admitted")
	}
	live := "view-source:https://vault.sliccy.ai/doc"
	w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
	w.browser(arAttached("", arSession, arTarget, "page", live, false))
	w.browser(arReply(arAttach, arSession))
	w.browser(arDetached(arSession, arTarget))
	w.answerAsks(live)
	t.Logf("client=%q", w.clientStrings())
	t.Logf("chrome=%q", w.chromeStrings())
	w.mustRefused(arAttach, arSession)
}

type arURL struct {
	name string
	url  string
}

func arProtectedURLs() []arURL {
	return []arURL{
		{"vault-view-source", "view-source:https://vault.sliccy.ai/doc"},
		{"seven-view-source", "view-source:https://seven.sliccy.ai/"},
		{"vault-blob", "blob:https://vault.sliccy.ai/0c1d2e3f-4a5b-6c7d-8e9f-001122334455"},
		{"vault-filesystem", "filesystem:https://vault.sliccy.ai/temporary/x.html"},
		{"vault-origin", "https://vault.sliccy.ai/doc"},
		{"seven-origin", "https://seven.sliccy.ai/"},
	}
}

func (w *arWire) mustOneRefusal(id int64, session string) {
	w.t.Helper()
	answers := w.answersFor(id)
	want := strings.Replace(arRefusal, `"id":70`, `"id":`+itoa(id), 1)
	if len(answers) != 1 || answers[0] != want {
		w.t.Fatalf("attach %d: want exactly %s, got %q (client=%q)", id, want, answers, w.clientStrings())
	}
	if got := w.clientMentions(session); len(got) != 0 {
		w.t.Fatalf("the session Chrome created reached the client: %q", got)
	}
	if w.registered(session) {
		w.t.Fatal("the session Chrome created was registered as a client session")
	}
	if n := w.detachesOf(session); n != 1 {
		w.t.Fatalf("want exactly one detach of the session Chrome created, got %d: %q", n, w.chromeStrings())
	}
}

func TestAttachReplyProtectedMeanwhile(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		for _, order := range []string{"event-reply-detach-answer", "event-reply-answer-detach", "reply-event-detach-answer", "reply-only", "event-detach-reply"} {
			t.Run(u.name+"/"+order, func(t *testing.T) {
				w := arNew(t)
				w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
				if !w.attach(arAttach, arTarget) {
					t.Fatal("attach not admitted")
				}
				w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
				switch order {
				case "event-reply-detach-answer":
					w.browser(arAttached("", arSession, arTarget, "page", live, false))
					w.browser(arReply(arAttach, arSession))
					w.browser(arDetached(arSession, arTarget))
					w.answerAsks(live)
				case "event-reply-answer-detach":
					w.browser(arAttached("", arSession, arTarget, "page", live, false))
					w.browser(arReply(arAttach, arSession))
					w.answerAsks(live)
					w.browser(arDetached(arSession, arTarget))
				case "reply-event-detach-answer":
					w.browser(arReply(arAttach, arSession))
					w.browser(arAttached("", arSession, arTarget, "page", live, false))
					w.browser(arDetached(arSession, arTarget))
					w.answerAsks(live)
				case "reply-only":
					w.browser(arReply(arAttach, arSession))
					w.answerAsks(live)
				case "event-detach-reply":
					w.browser(arAttached("", arSession, arTarget, "page", live, false))
					w.browser(arDetached(arSession, arTarget))
					w.browser(arReply(arAttach, arSession))
					w.answerAsks(live)
				}
				w.answerAsks(live)
				w.mustOneRefusal(arAttach, arSession)
			})
		}
	}
}

func TestAttachReplyProtectedBeforeAdmitUnchanged(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
			if w.attach(arAttach, arTarget) {
				t.Fatal("an attach to a target already protected must be refused at the command")
			}
			if got := w.answersFor(arAttach); len(got) != 1 || got[0] != arRefusal {
				t.Fatalf("want the standard refusal at the command, got %q", got)
			}
			if len(w.chrome) != 0 {
				t.Fatalf("nothing may reach Chrome: %q", w.chromeStrings())
			}
		})
	}
}

func TestAttachReplyReplyBeforeProtectThenConfirmProtected(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.browser(arReply(arAttach, arSession))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
			w.answerAsks(live)
			w.mustOneRefusal(arAttach, arSession)
		})
	}
}

func TestAttachReplyCommitOnOtherSession(t *testing.T) {
	for _, u := range arProtectedURLs()[4:] {
		live := u.url
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks("about:blank")
			if got := w.answersFor(arAttach); len(got) != 1 || !strings.Contains(got[0], arSession) {
				t.Fatalf("the first attach to a blank page must be answered with its session: %q", got)
			}
			second, sid2 := int64(arAttach+1), "S-AR-2"
			if !w.attach(second, arTarget) {
				t.Fatal("second attach not admitted")
			}
			origin := strings.TrimSuffix(strings.TrimSuffix(live, "/doc"), "/")
			w.browser(encode(map[string]any{"sessionId": arSession, "method": "Page.frameNavigated", "params": map[string]any{"frame": map[string]any{"id": arTarget, "loaderId": "L2", "url": live, "securityOrigin": origin, "mimeType": "text/html"}}}))
			w.browser(arAttached("", sid2, arTarget, "page", "about:blank", false))
			w.browser(arReply(second, sid2))
			w.browser(arDetached(sid2, arTarget))
			w.answerAsks(live)
			w.mustOneRefusal(second, sid2)
		})
	}
}

func TestAttachReplyHeldThenReplayedAnnounceThenOrdinaryConfirm(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.browser(arReply(arAttach, arSession))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
			w.browser(arAttached("", arSession, arTarget, "page", live, false))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", "https://ordinary.test/next"))
			w.answerAsks("https://ordinary.test/next")
			w.mustOneRefusal(arAttach, arSession)
		})
	}
}

func TestAttachReplyRefusedSessionReannounced(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks("about:blank")
			w.g.mu.Lock()
			delete(w.g.navTargets, arTarget)
			w.g.mu.Unlock()
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", "https://ordinary.test/next"))
			w.answerAsks("https://ordinary.test/next")
			before := len(w.client)
			w.browser(arAttached("", arSession, arTarget, "page", "https://ordinary.test/next", false))
			for _, raw := range w.client[before:] {
				if strings.Contains(string(raw), "attachedToTarget") {
					t.Fatalf("a re-announce of a refused session reached the client: %s", raw)
				}
			}
			w.g.mu.Lock()
			s := w.g.sessions[arSession]
			refused := s == nil || s.refused
			w.g.mu.Unlock()
			if !refused {
				t.Fatal("a re-announce turned a refused session back into an ordinary client session")
			}
		})
	}
}

func TestAttachReplyAutoAttachAfterProtect(t *testing.T) {
	for _, u := range arProtectedURLs() {
		live := u.url
		for _, shape := range []string{"page-root", "page-empty-hold", "tab-child"} {
			t.Run(u.name+"/"+shape, func(t *testing.T) {
				w := arNew(t)
				if shape != "page-empty-hold" {
					w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
				}
				switch shape {
				case "page-root":
					w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
					w.browser(arAttached("", arSession, arTarget, "page", live, true))
				case "page-empty-hold":
					w.browser(arAttached("", arSession, arTarget, "page", "", true))
					w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
				case "tab-child":
					w.browser(arAttached("", "S-TAB", "TAB-1", "tab", "", false))
					w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", live))
					w.browser(arAttached("S-TAB", arSession, arTarget, "page", live, true))
				}
				for _, raw := range w.client {
					_, _, method, _, params := arFields(raw)
					if method == "Target.attachedToTarget" && params["sessionId"] == arSession {
						t.Fatalf("an auto-attach announce for a protected target reached the client: %s", raw)
					}
				}
				if w.registered(arSession) {
					t.Fatal("an auto-attached session on a protected target was registered")
				}
				if w.detachesOf(arSession) != 1 {
					t.Fatalf("want exactly one detach: %q", w.chromeStrings())
				}
			})
		}
	}
}

const (
	arHeldTarget = "T-HELD"
	arHeldSess   = "S-HELD"
	arHeldTab    = "S-HELD-TAB"
	arHeldID     = 90
)

type arHold struct {
	name    string
	id      int64
	session string
	setup   func(w *arWire, protected bool)
	present func(g *guard) bool
	expire  func(w *arWire)
}

func (w *arWire) flushInto() {
	w.g.mu.Lock()
	w.g.flushAsync = func(send, page [][]byte) {
		w.take(browserVerdict{send: send, page: page})
	}
	w.g.mu.Unlock()
}

func arSeedSession(w *arWire, protected bool) {
	g := w.g
	g.mu.Lock()
	defer g.mu.Unlock()
	url := "https://ordinary.test/page"
	if protected {
		url = "https://vault.sliccy.ai/doc"
	}
	g.targets[arHeldTarget] = &target{id: arHeldTarget, typ: "page", url: url, protected: protected}
	g.targets["T-HELD-TAB"] = &target{id: "T-HELD-TAB", typ: "tab", url: url}
	g.sessions[arHeldTab] = &session{id: arHeldTab, target: "T-HELD-TAB", typ: "tab"}
	g.sessions[arHeldSess] = &session{id: arHeldSess, target: arHeldTarget, parent: arHeldTab, typ: "page", refused: protected}
	if protected {
		g.addRefusedLocked(arHeldSess)
	}
}

func arHeldReply(id int64, session string) message {
	return message{ID: &id, SessionID: session, Result: json.RawMessage(`{"result":{"type":"string","value":"x"}}`)}
}

func arHolds() []arHold {
	return []arHold{
		{
			name: "doc-confirm-attach-reply", id: arAttach, session: "",
			setup: func(w *arWire, protected bool) {
				w.browser(arRelay("Target.targetCreated", arHeldTarget, "page", "about:blank"))
				w.attach(arAttach, arHeldTarget)
				w.browser(arAttached("", arHeldSess, arHeldTarget, "page", "about:blank", false))
				w.browser(arReply(arAttach, arHeldSess))
				w.asks = nil
				if protected {
					w.g.mu.Lock()
					w.g.addRefusedLocked(arHeldSess)
					w.g.mu.Unlock()
				}
			},
			present: func(g *guard) bool {
				dc := g.docInflight[arHeldTarget]
				return dc != nil && !dc.done && dc.holds[arHeldSess] != nil && dc.holds[arHeldSess].hasReply
			},
			expire: func(w *arWire) {
				w.g.mu.Lock()
				if dc := w.g.docInflight[arHeldTarget]; dc != nil {
					dc.deadline = time.Now().Add(-time.Second)
				}
				w.g.mu.Unlock()
				w.g.expireDocConfirm(arHeldTarget)
			},
		},
		{
			name: "document-identity-reply", id: arHeldID, session: arHeldSess,
			setup: func(w *arWire, protected bool) {
				arSeedSession(w, protected)
				w.g.mu.Lock()
				m := arHeldReply(arHeldID, arHeldSess)
				w.g.docIdentityHolds[callKey{arHeldSess, arHeldID}] = &docIdentityHold{id: arHeldID, session: arHeldSess, target: arHeldTarget, method: "Runtime.evaluate", data: encode(m), arrived: time.Now(), deadline: time.Now().Add(-time.Second)}
				w.g.mu.Unlock()
			},
			present: func(g *guard) bool { return g.docIdentityHolds[callKey{arHeldSess, arHeldID}] != nil },
			expire:  func(w *arWire) { w.g.expireDocIdentityHold(callKey{arHeldSess, arHeldID}) },
		},
		{
			name: "body-answer", id: arHeldID, session: arHeldSess,
			setup: func(w *arWire, protected bool) {
				arSeedSession(w, protected)
				w.g.mu.Lock()
				m := arHeldReply(arHeldID, arHeldSess)
				w.g.heldAnswers[callKey{arHeldSess, arHeldID}] = &heldAnswer{call: bodyCall{req: requestKey{arHeldSess, "R-HELD"}, need: levelSettled}, m: m, data: encode(m)}
				w.g.mu.Unlock()
			},
			present: func(g *guard) bool { return g.heldAnswers[callKey{arHeldSess, arHeldID}] != nil },
			expire: func(w *arWire) {
				page, send, _ := w.g.expireAnswer(callKey{arHeldSess, arHeldID})
				w.take(browserVerdict{send: send, page: page})
			},
		},
		{
			name: "extension-paint", id: arHeldID, session: arHeldSess,
			setup: func(w *arWire, protected bool) {
				arSeedSession(w, protected)
				w.g.mu.Lock()
				m := arHeldReply(arHeldID, arHeldSess)
				w.g.heldExtensionPaints[callKey{arHeldSess, arHeldID}] = &heldExtensionPaint{m: m, data: encode(m), session: arHeldSess, target: arHeldTarget, deadline: time.Now().Add(-time.Second), generation: 1}
				w.g.mu.Unlock()
			},
			present: func(g *guard) bool { return g.heldExtensionPaints[callKey{arHeldSess, arHeldID}] != nil },
			expire: func(w *arWire) {
				page, _ := w.g.expireExtensionPaint(callKey{arHeldSess, arHeldID}, 1)
				w.take(browserVerdict{page: page})
			},
		},
	}
}

type arTeardown struct {
	name string
	run  func(w *arWire, h arHold)
}

func arTeardowns() []arTeardown {
	return []arTeardown{
		{"session-detached", func(w *arWire, h arHold) { w.browser(arDetached(arHeldSess, arHeldTarget)) }},
		{"parent-detached", func(w *arWire, h arHold) { w.browser(arDetached(arHeldTab, "T-HELD-TAB")) }},
		{"target-destroyed", func(w *arWire, h arHold) {
			w.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": arHeldTarget}}))
		}},
		{"target-destroyed-root", func(w *arWire, h arHold) {
			w.browser(encode(map[string]any{"method": "Target.targetDestroyed", "params": map[string]any{"targetId": arHeldTarget}}))
		}},
		{"expiry", func(w *arWire, h arHold) { h.expire(w) }},
		{"tab-cleanup", func(w *arWire, h arHold) {
			w.browser(arRelay("Target.targetCreated", "T-PROT", "page", "https://vault.sliccy.ai/doc"))
			w.browser(arAttached(arHeldTab, "S-PROT", "T-PROT", "page", "https://vault.sliccy.ai/doc", false))
		}},
		{"held-page-expiry-under-tab", func(w *arWire, h arHold) {
			w.g.mu.Lock()
			w.g.targets["T-EMPTY"] = &target{id: "T-EMPTY", typ: "page"}
			w.g.held["S-EMPTY"] = &heldSession{id: "S-EMPTY", parent: arHeldTab, target: "T-EMPTY", typ: "page", at: time.Now()}
			w.g.mu.Unlock()
			page, send := w.g.expireHold("S-EMPTY")
			w.take(browserVerdict{send: send, page: page})
		}},
	}
}

func (w *arWire) answersOn(id int64, session string) []string {
	var out []string
	for _, raw := range w.client {
		got, has, method, s, _ := arFields(raw)
		if has && method == "" && got == id && s == session {
			out = append(out, string(raw))
		}
	}
	return out
}

func TestHeldReplyAnsweredOnTeardown(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "1h")
	for _, h := range arHolds() {
		for _, protected := range []bool{false, true} {
			for _, first := range arTeardowns() {
				name := h.name + "/ordinary/" + first.name
				if protected {
					name = h.name + "/protected/" + first.name
				}
				t.Run(name, func(t *testing.T) {
					w := arNew(t)
					w.flushInto()
					h.setup(w, protected)
					w.g.mu.Lock()
					present := h.present(w.g)
					w.g.mu.Unlock()
					if !present {
						t.Fatal("the hold was not set up")
					}
					if got := w.answersOn(h.id, h.session); len(got) != 0 {
						t.Fatalf("the held reply was answered before any teardown: %q", got)
					}
					order := []arTeardown{first}
					for _, other := range arTeardowns() {
						if other.name != first.name {
							order = append(order, other)
						}
					}
					for i, step := range order {
						step.run(w, h)
						w.g.mu.Lock()
						still := h.present(w.g)
						w.g.mu.Unlock()
						got := w.answersOn(h.id, h.session)
						if !still && len(got) == 0 {
							t.Fatalf("step %d %s removed the held reply without answering it (client=%q)", i, step.name, w.clientStrings())
						}
						if len(got) > 1 {
							t.Fatalf("step %d %s: the held reply was answered %d times: %q", i, step.name, len(got), got)
						}
						if len(got) == 1 && !strings.Contains(got[0], `"error"`) && !strings.Contains(got[0], `"result"`) {
							t.Fatalf("not an answer: %s", got[0])
						}
					}
					got := w.answersOn(h.id, h.session)
					if len(got) != 1 {
						t.Fatalf("want exactly one answer after every teardown, got %q", got)
					}
					if strings.Contains(got[0], `"error"`) && !strings.Contains(got[0], "slicc: refused: ") && !strings.Contains(got[0], refusedDocRetriable) {
						t.Fatalf("an answer that is neither the real result nor a guard refusal: %s", got[0])
					}
				})
			}
		}
	}
}

func (w *arWire) transcript() string {
	return strings.Join(w.clientStrings(), "\n") + "\n||\n" + strings.Join(w.chromeStrings(), "\n")
}

func arStockScenarios() map[string]func(w *arWire) {
	ordinary := "https://ordinary.test/page"
	return map[string]func(w *arWire){
		"blank-event-reply-answer": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks("about:blank")
		},
		"ordinary-event-answer-reply": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
			w.answerAsks(ordinary)
			w.browser(arReply(arAttach, arSession))
		},
		"ordinary-reply-first": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.attach(arAttach, arTarget)
			w.browser(arReply(arAttach, arSession))
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
			w.answerAsks(ordinary)
		},
		"other-target-protected-meanwhile": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.browser(arRelay("Target.targetCreated", "OTHER", "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arRelay("Target.targetInfoChanged", "OTHER", "page", "view-source:https://vault.sliccy.ai/doc"))
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks(ordinary)
		},
		"duplicate-reply-and-replayed-announce": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks(ordinary)
			w.browser(arReply(arAttach, arSession))
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
		},
		"session-detached-after-pass": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, false))
			w.browser(arReply(arAttach, arSession))
			w.answerAsks(ordinary)
			w.browser(arDetached(arSession, arTarget))
		},
		"auto-attach-ordinary": func(w *arWire) {
			w.browser(arRelay("Target.targetCreated", arTarget, "page", ordinary))
			w.browser(arAttached("", arSession, arTarget, "page", ordinary, true))
		},
		"auto-attach-empty-then-ordinary": func(w *arWire) {
			w.browser(arAttached("", arSession, arTarget, "page", "", true))
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", ordinary))
		},
	}
}

func TestAttachReplyStockTranscripts(t *testing.T) {
	for name, run := range arStockScenarios() {
		t.Run(name, func(t *testing.T) {
			w := arNew(t)
			run(w)
			got := w.transcript()
			if os.Getenv("AR_PRINT") != "" {
				t.Logf("GOLDEN %s %s", name, strconv.Quote(got))
				return
			}
			want, ok := arStockGolden[name]
			if !ok {
				t.Fatalf("no golden transcript for %s", name)
			}
			if got != want {
				t.Fatalf("stock transcript changed\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

var arStockGolden = map[string]string{
	"auto-attach-empty-then-ordinary":       "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n||\n{\"id\":1879048193,\"method\":\"Runtime.runIfWaitingForDebugger\",\"sessionId\":\"S-AR\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"auto-attach-ordinary":                  "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":true}}\n||\n{\"id\":1879048193,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"blank-event-reply-answer":              "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"about:blank\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"duplicate-reply-and-replayed-announce": "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"ordinary-event-answer-reply":           "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"ordinary-reply-first":                  "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"other-target-protected-meanwhile":      "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
	"session-detached-after-pass":           "{\"method\":\"Target.attachedToTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetInfo\":{\"browserContextId\":\"CTX\",\"targetId\":\"AR-TARGET\",\"type\":\"page\",\"url\":\"https://ordinary.test/page\"},\"waitingForDebugger\":false}}\n{\"id\":70,\"result\":{\"sessionId\":\"S-AR\"}}\n{\"method\":\"Target.detachedFromTarget\",\"params\":{\"sessionId\":\"S-AR\",\"targetId\":\"AR-TARGET\"}}\n||\n{\"id\":70,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"}}\n{\"id\":1879048193,\"method\":\"Target.getTargetInfo\",\"params\":{\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}\n{\"id\":1879048194,\"method\":\"Target.attachToTarget\",\"params\":{\"flatten\":true,\"targetId\":\"AR-TARGET\"},\"sessionId\":\"RELAY\"}",
}

func TestAttachReplyPassedThenProtectedThenReply(t *testing.T) {
	for _, u := range arProtectedURLs() {
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arAttached("", arSession, arTarget, "page", "about:blank", false))
			w.answerAsks("about:blank")
			w.g.mu.Lock()
			delete(w.g.navTargets, arTarget)
			w.g.mu.Unlock()
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", u.url))
			w.browser(arReply(arAttach, arSession))
			answers := w.answersFor(arAttach)
			if len(answers) != 1 || answers[0] != arRefusal {
				t.Fatalf("want exactly the standard refusal, got %q", answers)
			}
			if n := w.detachesOf(arSession); n != 1 {
				t.Fatalf("want exactly one detach, got %d: %q", n, w.chromeStrings())
			}
		})
	}
}

func TestHeldReplyOnEmptyTabAnsweredAtOnce(t *testing.T) {
	w := arNew(t)
	w.g.mu.Lock()
	w.g.targets["T-TAB"] = &target{id: "T-TAB", typ: "tab"}
	w.g.sessions["S-TAB"] = &session{id: "S-TAB", target: "T-TAB", typ: "tab", emptyAtAnnounce: true, at: time.Now()}
	m := arHeldReply(arHeldID, "S-TAB")
	w.g.heldAnswers[callKey{"S-TAB", arHeldID}] = &heldAnswer{call: bodyCall{req: requestKey{"S-TAB", "R-TAB"}, need: levelSettled}, m: m, data: encode(m)}
	w.g.mu.Unlock()
	w.browser(arRelay("Target.targetCreated", "T-PROT", "page", "https://vault.sliccy.ai/doc"))
	before := len(w.client)
	w.browser(arAttached("", "S-PROT", "T-PROT", "page", "https://vault.sliccy.ai/doc", false))
	w.g.mu.Lock()
	gone := w.g.heldAnswers[callKey{"S-TAB", arHeldID}] == nil
	w.g.mu.Unlock()
	if !gone {
		t.Fatal("the empty tab was not cleaned up in this model")
	}
	var got []string
	for _, raw := range w.client[before:] {
		id, has, method, session, _ := arFields(raw)
		if has && method == "" && id == arHeldID && session == "S-TAB" {
			got = append(got, string(raw))
		}
	}
	if len(got) != 1 {
		t.Fatalf("the reply parked on the cleaned-up tab must be answered by that same message, got %q (client=%q)", got, w.clientStrings())
	}
}

func TestAttachReplyCutSessionReannouncedAfterClear(t *testing.T) {
	for _, u := range arProtectedURLs() {
		t.Run(u.name, func(t *testing.T) {
			w := arNew(t)
			w.browser(arRelay("Target.targetCreated", arTarget, "page", "about:blank"))
			w.attach(arAttach, arTarget)
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", u.url))
			w.browser(arReply(arAttach, arSession))
			w.mustOneRefusal(arAttach, arSession)
			w.browser(arDetached(arSession, arTarget))
			w.g.mu.Lock()
			w.g.clearDocFailedSessionLocked(arSession)
			w.g.mu.Unlock()
			w.browser(arRelay("Target.targetInfoChanged", arTarget, "page", "https://ordinary.test/next"))
			w.answerAsks("https://ordinary.test/next")
			before := len(w.client)
			w.browser(arAttached("", arSession, arTarget, "page", "https://ordinary.test/next", false))
			for _, raw := range w.client[before:] {
				if strings.Contains(string(raw), "attachedToTarget") {
					t.Fatalf("a replayed announce of a session refused at its attach answer reached the client: %s", raw)
				}
			}
			if w.registered(arSession) {
				t.Fatal("a session refused at its attach answer was registered by a replayed announce")
			}
		})
	}
}
