package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	hlPage        = "HL-PAGE"
	hlPageSession = "S-HL-PAGE"
	hlFrame       = "HL-FRAME"
	hlFrameSess   = "S-HL-FRAME"
	hlPageURL     = "https://page.ordinary.test/framed"
	hlFrameURL    = "https://frame.other.test/inner?in=framed"
	hlVaultURL    = "https://vault.sliccy.ai/inner"
	hlSevenURL    = "https://seven.sliccy.ai/doc"
	hlSecret      = "HL-UNCLASSIFIED-TITLE"
	hlWait        = 3 * time.Second
)

type hlWire struct {
	t                 *testing.T
	g                 *guard
	mu                sync.Mutex
	client            [][]byte
	late              [][]byte
	lateAfterDeadline []bool
	chrome            [][]byte
	logs              []string
	notify            chan struct{}
}

func hlNew(t *testing.T, pageURL string) *hlWire {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	w := &hlWire{t: t, notify: make(chan struct{}, 4096)}
	w.g = newGuard(o, func(format string, args ...any) {
		w.mu.Lock()
		w.logs = append(w.logs, fmt.Sprintf(format, args...))
		w.mu.Unlock()
	})
	w.g.relaySession = "RELAY"
	w.g.flushAsync = func(send, page [][]byte) {
		w.mu.Lock()
		w.chrome = append(w.chrome, send...)
		for _, raw := range page {
			w.late = append(w.late, raw)
			w.lateAfterDeadline = append(w.lateAfterDeadline, w.deadlineLoggedLocked())
		}
		w.mu.Unlock()
		for range page {
			w.notify <- struct{}{}
		}
	}
	w.g.observeAll([]targetInfo{{TargetID: hlPage, Type: "page", URL: pageURL, BrowserContextID: "CTX"}})
	if !w.g.targets[hlPage].protected {
		w.g.sessions[hlPageSession] = &session{id: hlPageSession, target: hlPage, typ: "page"}
		w.g.navTargets[hlPage] = &navTarget{target: hlPage, session: "GUARD", ready: make(chan struct{}), done: true}
	}
	return w
}

func (w *hlWire) deadlineLoggedLocked() bool {
	for _, line := range w.logs {
		if strings.Contains(line, "reached the hold's deadline") {
			return true
		}
	}
	return false
}

func hlFrameInfo(frame, url, title string) map[string]any {
	return map[string]any{"targetId": frame, "type": "iframe", "title": title, "url": url, "attached": false, "canAccessOpener": false, "browserContextId": "CTX", "parentId": hlPage, "parentFrameId": hlPage}
}

func (w *hlWire) take(v browserVerdict) browserVerdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v.forward != nil {
		w.client = append(w.client, v.forward)
	}
	w.client = append(w.client, v.page...)
	w.chrome = append(w.chrome, v.send...)
	return v
}

func (w *hlWire) browser(raw []byte) browserVerdict {
	return w.take(w.g.fromBrowser(raw))
}

func (w *hlWire) relay(method string, info map[string]any) browserVerdict {
	return w.browser(encode(map[string]any{"sessionId": "RELAY", "method": method, "params": map[string]any{"targetInfo": info}}))
}

func (w *hlWire) root(method string, info map[string]any) browserVerdict {
	return w.browser(encode(map[string]any{"method": method, "params": map[string]any{"targetInfo": info}}))
}

func (w *hlWire) announce(frame string) {
	w.relay("Target.targetCreated", hlFrameInfo(frame, "", ""))
	w.root("Target.targetCreated", hlFrameInfo(frame, "", ""))
}

func (w *hlWire) hold(frame, sid string, waiting bool) {
	w.t.Helper()
	info := hlFrameInfo(frame, "", "")
	info["attached"] = true
	w.browser(encode(map[string]any{"method": "Target.attachedToTarget", "sessionId": hlPageSession, "params": map[string]any{"sessionId": sid, "waitingForDebugger": waiting, "targetInfo": info}}))
	w.g.mu.Lock()
	held := w.g.iframeHeldLocked(frame)
	w.g.mu.Unlock()
	if !held {
		w.t.Fatalf("the URL-less iframe %s was not held", frame)
	}
}

func (w *hlWire) learnURL(frame, url string) browserVerdict {
	return w.relay("Target.targetInfoChanged", hlFrameInfo(frame, url, ""))
}

type hlLookup struct {
	id     int64
	parked <-chan struct{}
	pv     pageVerdict
	c      *command
	n      int64
}

func (w *hlWire) lookup(id int64, session, frame string) *hlLookup {
	raw := map[string]any{"id": id, "method": "Target.getTargetInfo", "params": map[string]any{"targetId": frame}}
	if session != "" {
		raw["sessionId"] = session
	}
	c, n, ok := parseCommand(encode(raw))
	if !ok {
		w.t.Fatal("parse Target.getTargetInfo")
	}
	l := &hlLookup{id: id, c: c, n: n}
	if ch := w.g.commandWait(c); ch != nil {
		l.parked = ch
		return l
	}
	w.admit(l)
	return l
}

func (w *hlWire) admit(l *hlLookup) {
	l.pv = w.g.fromCommand(l.c, l.n, true)
	w.mu.Lock()
	defer w.mu.Unlock()
	if l.pv.reply != nil {
		w.client = append(w.client, l.pv.reply)
	}
	if l.pv.forward != nil {
		w.chrome = append(w.chrome, l.pv.forward)
	}
	w.chrome = append(w.chrome, l.pv.send...)
}

func (w *hlWire) resume(l *hlLookup) {
	w.t.Helper()
	select {
	case <-l.parked:
	case <-time.After(hlWait):
		w.t.Fatalf("the parked lookup %d never resumed", l.id)
	}
	for ch := w.g.commandWait(l.c); ch != nil; ch = w.g.commandWait(l.c) {
		<-ch
	}
	l.parked = nil
	w.admit(l)
}

func hlChromeReply(id int64, session, frame, url, title string) []byte {
	s := fmt.Sprintf(`{"id":%d,"result":{"targetInfo":{"targetId":%q,"type":"iframe","title":%q,"url":%q,"attached":true,"canAccessOpener":false,"browserContextId":"CTX","parentFrameId":%q}}`, id, frame, title, url, hlPage)
	if session != "" {
		s += fmt.Sprintf(`,"sessionId":%q`, session)
	}
	return []byte(s + "}")
}

func hlMissing(id int64, session string) string {
	return string(missingTarget(id, session))
}

func hlFields(raw []byte) (int64, bool, string) {
	var m struct {
		ID        *int64 `json:"id"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.ID == nil {
		return 0, false, m.SessionID
	}
	return *m.ID, true, m.SessionID
}

func (w *hlWire) answers(id int64, session string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, raw := range append(append([][]byte{}, w.client...), w.late...) {
		if got, has, s := hlFields(raw); has && got == id && s == session {
			out = append(out, string(raw))
		}
	}
	return out
}

func (w *hlWire) lateCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.late)
}

func (w *hlWire) waitLate(n int) {
	w.t.Helper()
	deadline := time.After(hlWait)
	for w.lateCount() < n {
		select {
		case <-w.notify:
		case <-deadline:
			w.t.Fatalf("want %d answers after the hold, got %d: client=%q late=%q", n, w.lateCount(), w.clientStrings(), w.lateStrings())
		}
	}
}

func (w *hlWire) settleQuiet() {
	time.Sleep(100 * time.Millisecond)
}

func (w *hlWire) clientStrings() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.client))
	for _, raw := range w.client {
		out = append(out, string(raw))
	}
	return out
}

func (w *hlWire) lateStrings() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.late))
	for _, raw := range w.late {
		out = append(out, string(raw))
	}
	return out
}

func (w *hlWire) mentions(s string) []string {
	var out []string
	for _, raw := range append(w.clientStrings(), w.lateStrings()...) {
		if strings.Contains(raw, s) {
			out = append(out, raw)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range w.logs {
		if strings.Contains(line, s) {
			out = append(out, "log: "+line)
		}
	}
	return out
}

func (w *hlWire) logged(s string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range w.logs {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func (w *hlWire) noSlotsLeft() {
	w.t.Helper()
	w.g.mu.Lock()
	defer w.g.mu.Unlock()
	for id, hw := range w.g.heldWaits {
		if len(hw.calls) != 0 {
			w.t.Fatalf("a settled wait for %s still holds %d slots", id, len(hw.calls))
		}
	}
	if len(w.g.parkedAnswers) != 0 {
		w.t.Fatalf("parked answers left after every lookup was answered: %v", w.g.parkedAnswers)
	}
}

func (w *hlWire) mustOne(id int64, session, want string) {
	w.t.Helper()
	got := w.answers(id, session)
	if len(got) != 1 || got[0] != want {
		w.t.Fatalf("lookup %d: want exactly %s, got %q (client=%q late=%q)", id, want, got, w.clientStrings(), w.lateStrings())
	}
}

func (w *hlWire) mustUnanswered(id int64, session, when string) {
	w.t.Helper()
	if got := w.answers(id, session); len(got) != 0 {
		w.t.Fatalf("lookup %d was answered %s: %q", id, when, got)
	}
}

func hlReleasedBytes(t *testing.T, id int64, session string, reply []byte) string {
	t.Helper()
	w := hlNew(t, hlPageURL)
	if session != "" {
		w.g.browserSessions[session] = true
	}
	w.announce(hlFrame)
	w.hold(hlFrame, hlFrameSess, true)
	w.learnURL(hlFrame, hlFrameURL)
	l := w.lookup(id, session, hlFrame)
	if l.parked != nil || l.pv.forward == nil {
		t.Fatalf("the lookup after the release was not forwarded: %+v", l.pv)
	}
	v := w.browser(reply)
	if v.forward == nil {
		t.Fatalf("the lookup after the release was not answered at once")
	}
	return string(v.forward)
}

func (w *hlWire) parkedReply(id int64, session, url, title string) {
	w.t.Helper()
	l := w.lookup(id, session, hlFrame)
	if l.parked != nil || l.pv.forward == nil {
		w.t.Fatalf("the lookup %d before the hold was not forwarded to Chrome: %+v", id, l.pv)
	}
	w.hold(hlFrame, hlFrameSess, true)
	w.browser(hlChromeReply(id, session, hlFrame, url, title))
	w.mustUnanswered(id, session, "while its iframe was held")
}

func TestHeldLookupReplyParkedUntilRelease(t *testing.T) {
	for _, row := range []struct {
		name, session, replyURL string
	}{
		{"reply-without-url", "", ""},
		{"reply-with-url", "", hlFrameURL},
		{"browser-session", "S-HL-BROWSER", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			reply := hlChromeReply(67, row.session, hlFrame, row.replyURL, hlSecret)
			want := hlReleasedBytes(t, 67, row.session, reply)
			w := hlNew(t, hlPageURL)
			if row.session != "" {
				w.g.browserSessions[row.session] = true
			}
			w.announce(hlFrame)
			w.parkedReply(67, row.session, row.replyURL, hlSecret)
			if !w.logged("holding the answer to Target.getTargetInfo") {
				t.Fatalf("the reply was not parked: logs=%q", w.logs)
			}
			w.learnURL(hlFrame, hlFrameURL)
			w.waitLate(1)
			w.settleQuiet()
			w.mustOne(67, row.session, want)
			if !strings.Contains(want, hlSecret) {
				t.Fatalf("the released answer lost Chrome's info: %s", want)
			}
			if string(reply) == want {
				t.Fatalf("the twin delivered Chrome's bytes untouched; the row can't tell a raw answer from a classified one")
			}
			w.noSlotsLeft()
		})
	}
}

func TestHeldLookupReplyBothHoldsReleased(t *testing.T) {
	reply := hlChromeReply(67, "", hlFrame, "", hlSecret)
	want := hlReleasedBytes(t, 67, "", reply)
	w := hlNew(t, hlPageURL)
	w.g.sessions["S-HL-PRIVATE"] = &session{id: "S-HL-PRIVATE", target: hlPage, typ: "page"}
	w.announce(hlFrame)
	l := w.lookup(67, "", hlFrame)
	if l.pv.forward == nil {
		t.Fatal("lookup not forwarded")
	}
	w.hold(hlFrame, hlFrameSess, true)
	info := hlFrameInfo(hlFrame, "", "")
	w.browser(encode(map[string]any{"method": "Target.attachedToTarget", "sessionId": "S-HL-PRIVATE", "params": map[string]any{"sessionId": "S-HL-FRAME-2", "waitingForDebugger": false, "targetInfo": info}}))
	w.browser(reply)
	w.mustUnanswered(67, "", "while its iframe was held")
	w.learnURL(hlFrame, hlFrameURL)
	w.waitLate(1)
	w.settleQuiet()
	w.mustOne(67, "", want)
	w.noSlotsLeft()
}

func (w *hlWire) answerGuard(pageURL string) {
	answered := map[int64]bool{}
	for progress := true; progress; {
		progress = false
		w.mu.Lock()
		asks := append([][]byte{}, w.chrome...)
		w.mu.Unlock()
		for _, raw := range asks {
			var m struct {
				ID        int64          `json:"id"`
				Method    string         `json:"method"`
				SessionID string         `json:"sessionId"`
				Params    map[string]any `json:"params"`
			}
			_ = json.Unmarshal(raw, &m)
			if m.ID < relayIDBase || answered[m.ID] || (m.SessionID != "GUARD" && m.SessionID != "RELAY") {
				continue
			}
			answered[m.ID] = true
			progress = true
			result := map[string]any{}
			switch m.Method {
			case "Page.getFrameTree":
				result = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": hlPage, "loaderId": "L", "url": pageURL, "securityOrigin": pageURL, "mimeType": "text/html"}}}
			case "Target.getTargetInfo":
				result = map[string]any{"targetInfo": map[string]any{"targetId": hlPage, "type": "page", "url": pageURL, "browserContextId": "CTX"}}
			}
			w.browser(encode(map[string]any{"id": m.ID, "sessionId": m.SessionID, "result": result}))
		}
	}
}

func (w *hlWire) parentMoves(url string) {
	w.relay("Target.targetInfoChanged", map[string]any{"targetId": hlPage, "type": "page", "url": url, "browserContextId": "CTX"})
	w.answerGuard(url)
}

func TestHeldLookupReplyProtectedAtSettle(t *testing.T) {
	for _, row := range []struct {
		name string
		step func(w *hlWire)
	}{
		{"own-url-vault", func(w *hlWire) { w.learnURL(hlFrame, hlVaultURL) }},
		{"own-url-seven", func(w *hlWire) { w.learnURL(hlFrame, hlSevenURL) }},
		{"parent-moves-to-seven", func(w *hlWire) {
			w.parentMoves(hlSevenURL)
			w.learnURL(hlFrame, hlFrameURL)
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := hlNew(t, hlPageURL)
			w.announce(hlFrame)
			w.parkedReply(67, "", "", hlSecret)
			row.step(w)
			w.waitLate(1)
			w.settleQuiet()
			w.mustOne(67, "", hlMissing(67, ""))
			w.noSlotsLeft()
		})
	}
}

func TestHeldLookupReplyProtectedAtSettleNoLeak(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	w.parkedReply(67, "", "", hlSecret)
	w.learnURL(hlFrame, hlVaultURL)
	w.waitLate(1)
	w.settleQuiet()
	if got := w.mentions(hlSecret); len(got) != 0 {
		t.Fatalf("Chrome's info for a protected iframe reached the client or the log: %q", got)
	}
}

func TestHeldLookupProtectedBothOrders(t *testing.T) {
	t.Run("protected-before-reply", func(t *testing.T) {
		w := hlNew(t, hlPageURL)
		w.announce(hlFrame)
		l := w.lookup(67, "", hlFrame)
		if l.pv.forward == nil {
			t.Fatal("lookup not forwarded")
		}
		w.hold(hlFrame, hlFrameSess, true)
		w.learnURL(hlFrame, hlVaultURL)
		before := w.lateCount()
		v := w.browser(hlChromeReply(67, "", hlFrame, hlVaultURL, hlSecret))
		if string(v.forward) != hlMissing(67, "") {
			t.Fatalf("a protected iframe's lookup was not refused at once: %s", v.forward)
		}
		w.settleQuiet()
		if w.lateCount() != before {
			t.Fatalf("a protected iframe's lookup was parked: %q", w.lateStrings())
		}
		w.mustOne(67, "", hlMissing(67, ""))
		if got := w.mentions(hlSecret); len(got) != 0 {
			t.Fatalf("Chrome's info for a protected iframe leaked: %q", got)
		}
	})
	t.Run("protected-before-lookup", func(t *testing.T) {
		w := hlNew(t, hlPageURL)
		w.announce(hlFrame)
		w.learnURL(hlFrame, hlVaultURL)
		l := w.lookup(67, "", hlFrame)
		if l.parked != nil || l.pv.forward != nil {
			t.Fatalf("a protected iframe's lookup reached Chrome: %+v", l.pv)
		}
		w.mustOne(67, "", hlMissing(67, ""))
	})
	t.Run("protected-parent-before-lookup", func(t *testing.T) {
		w := hlNew(t, hlSevenURL)
		w.relay("Target.targetCreated", hlFrameInfo(hlFrame, hlFrameURL, ""))
		l := w.lookup(67, "", hlFrame)
		if l.parked != nil || l.pv.forward != nil {
			t.Fatalf("an ordinary-URL iframe under seven reached Chrome: %+v", l.pv)
		}
		w.mustOne(67, "", hlMissing(67, ""))
	})
	t.Run("protected-parent-after-park", func(t *testing.T) {
		w := hlNew(t, hlPageURL)
		w.announce(hlFrame)
		w.parkedReply(67, "", "", hlSecret)
		w.parentMoves(hlSevenURL)
		w.learnURL(hlFrame, hlFrameURL)
		w.g.mu.Lock()
		frame := *w.g.targets[hlFrame]
		w.g.mu.Unlock()
		if !frame.protected || frame.url != hlFrameURL || w.g.urlVerdict(frame.url).protected {
			t.Fatalf("the twin must be protected by its parent only: protected=%t url=%s", frame.protected, frame.url)
		}
		w.waitLate(1)
		w.settleQuiet()
		w.mustOne(67, "", hlMissing(67, ""))
		if got := w.mentions(hlSecret); len(got) != 0 {
			t.Fatalf("Chrome's info for an iframe under seven leaked: %q", got)
		}
	})
}

func TestHeldLookupReplyEndsWithoutRelease(t *testing.T) {
	for _, row := range []struct {
		name string
		step func(w *hlWire)
	}{
		{"target-destroyed", func(w *hlWire) {
			w.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": hlFrame}}))
		}},
		{"held-session-detached", func(w *hlWire) {
			w.browser(encode(map[string]any{"sessionId": hlPageSession, "method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": hlFrameSess, "targetId": hlFrame}}))
		}},
		{"parent-session-detached", func(w *hlWire) {
			w.browser(encode(map[string]any{"method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": hlPageSession, "targetId": hlPage}}))
		}},
		{"parent-destroyed", func(w *hlWire) {
			w.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": hlPage}}))
			w.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": hlFrame}}))
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := hlNew(t, hlPageURL)
			w.announce(hlFrame)
			w.parkedReply(67, "", "", hlSecret)
			row.step(w)
			w.waitLate(1)
			for _, again := range []func(){
				func() { w.learnURL(hlFrame, hlFrameURL) },
				func() {
					w.browser(encode(map[string]any{"sessionId": hlPageSession, "method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": hlFrameSess, "targetId": hlFrame}}))
				},
				func() {
					w.browser(encode(map[string]any{"sessionId": "RELAY", "method": "Target.targetDestroyed", "params": map[string]any{"targetId": hlFrame}}))
				},
			} {
				again()
			}
			w.g.closeHeldWaits()
			w.settleQuiet()
			w.mustOne(67, "", hlMissing(67, ""))
			if got := w.mentions(hlSecret); len(got) != 0 {
				t.Fatalf("Chrome's info for an iframe that went away reached the client: %q", got)
			}
			w.noSlotsLeft()
		})
	}
}

func TestHeldLookupReplyBrowserSessionGone(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.g.browserSessions["S-HL-BROWSER"] = true
	w.announce(hlFrame)
	w.parkedReply(67, "S-HL-BROWSER", "", hlSecret)
	w.browser(encode(map[string]any{"method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": "S-HL-BROWSER"}}))
	w.mustUnanswered(67, "S-HL-BROWSER", "twice when its session went away")
	w.learnURL(hlFrame, hlFrameURL)
	w.waitLate(1)
	w.browser(encode(map[string]any{"method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": "S-HL-BROWSER"}}))
	w.g.closeHeldWaits()
	w.settleQuiet()
	w.mustOne(67, "S-HL-BROWSER", string(missingSession(67, "S-HL-BROWSER")))
	if got := w.mentions(hlSecret); len(got) != 0 {
		t.Fatalf("Chrome's info reached a session that went away: %q", got)
	}
	w.noSlotsLeft()
}

func TestHeldLookupReplyStreamEnds(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	w.parkedReply(67, "", "", hlSecret)
	w.g.closeHeldWaits()
	w.waitLate(1)
	w.learnURL(hlFrame, hlFrameURL)
	w.settleQuiet()
	w.mustOne(67, "", hlMissing(67, ""))
	if got := w.mentions(hlSecret); len(got) != 0 {
		t.Fatalf("Chrome's info reached the client after the stream ended: %q", got)
	}
}

func TestHeldLookupReplyDeadline(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "400ms")
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	w.parkedReply(67, "", "", hlSecret)
	w.waitLate(1)
	w.mu.Lock()
	afterDeadline := w.lateAfterDeadline[0]
	w.mu.Unlock()
	if !afterDeadline {
		t.Fatalf("the parked reply was answered before the hold's deadline: logs=%q late=%q", w.logs, w.lateStrings())
	}
	w.mustOne(67, "", hlMissing(67, ""))
	w.learnURL(hlFrame, hlFrameURL)
	w.settleQuiet()
	w.mustOne(67, "", hlMissing(67, ""))
	w.noSlotsLeft()
}

func TestHeldLookupReplyCaps(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	var ids []int64
	for i := int64(0); i <= maxParkedPerIframe; i++ {
		id := 100 + i
		ids = append(ids, id)
		if l := w.lookup(id, "", hlFrame); l.pv.forward == nil {
			t.Fatalf("lookup %d not forwarded", id)
		}
	}
	w.hold(hlFrame, hlFrameSess, true)
	for _, id := range ids {
		w.browser(hlChromeReply(id, "", hlFrame, "", hlSecret))
	}
	over := ids[len(ids)-1]
	w.mustOne(over, "", hlMissing(over, ""))
	for _, id := range ids[:len(ids)-1] {
		w.mustUnanswered(id, "", "while its iframe was held")
	}
	extra := w.lookup(200, "", hlFrame)
	if extra.parked != nil {
		t.Fatal("a lookup past the cap was parked")
	}
	w.mustOne(200, "", hlMissing(200, ""))
	w.learnURL(hlFrame, hlFrameURL)
	w.waitLate(maxParkedPerIframe)
	w.settleQuiet()
	for _, id := range ids[:len(ids)-1] {
		got := w.answers(id, "")
		if len(got) != 1 || !strings.Contains(got[0], hlSecret) {
			t.Fatalf("parked lookup %d after release: %q", id, got)
		}
	}
	w.noSlotsLeft()
}

func TestHeldLookupReplyCapsShareCommands(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	for i := int64(0); i < 4; i++ {
		w.lookup(100+i, "", hlFrame)
	}
	w.hold(hlFrame, hlFrameSess, true)
	var parked []*hlLookup
	for i := int64(0); i < 4; i++ {
		l := w.lookup(300+i, "", hlFrame)
		if l.parked == nil {
			t.Fatalf("lookup %d after the hold was not parked", 300+i)
		}
		parked = append(parked, l)
	}
	for i := int64(0); i < 4; i++ {
		w.browser(hlChromeReply(100+i, "", hlFrame, "", hlSecret))
	}
	w.mustUnanswered(103, "", "while its iframe was held")
	over := w.lookup(400, "", hlFrame)
	if over.parked != nil {
		t.Fatal("a lookup past the shared cap was parked")
	}
	w.mustOne(400, "", hlMissing(400, ""))
	w.learnURL(hlFrame, hlFrameURL)
	for _, l := range parked {
		w.resume(l)
		if l.pv.forward == nil {
			t.Fatalf("parked lookup %d was not forwarded after the release: %+v", l.id, l.pv)
		}
	}
	w.waitLate(4)
	w.settleQuiet()
	w.noSlotsLeft()
}

func TestHeldLookupReplySlotsReleasedOnSettle(t *testing.T) {
	w := hlNew(t, hlPageURL)
	frames := maxParkedPerPage / maxParkedPerIframe
	next := int64(1000)
	for f := 0; f < frames; f++ {
		frame := fmt.Sprintf("HL-FRAME-%d", f)
		w.announce(frame)
		var ids []int64
		for i := 0; i < maxParkedPerIframe; i++ {
			ids = append(ids, next)
			w.lookup(next, "", frame)
			next++
		}
		w.hold(frame, "S-"+frame, true)
		for _, id := range ids {
			w.browser(hlChromeReply(id, "", frame, "", hlSecret))
			w.mustUnanswered(id, "", "while its iframe was held")
		}
	}
	w.g.mu.Lock()
	var waits []*heldWait
	for f := 0; f < frames; f++ {
		waits = append(waits, w.g.heldWaits[fmt.Sprintf("HL-FRAME-%d", f)])
	}
	w.g.mu.Unlock()
	for _, hw := range waits {
		w.g.expireHeldWait(hw)
	}
	w.waitLate(frames * maxParkedPerIframe)
	last := "HL-FRAME-LAST"
	w.announce(last)
	w.lookup(next, "", last)
	w.hold(last, "S-"+last, true)
	w.browser(hlChromeReply(next, "", last, "", hlSecret))
	w.mustUnanswered(next, "", "at once although the expired waits had released their slots")
	w.learnURL(last, hlFrameURL)
	w.waitLate(frames*maxParkedPerIframe + 1)
	w.settleQuiet()
	got := w.answers(next, "")
	if len(got) != 1 || !strings.Contains(got[0], hlSecret) {
		t.Fatalf("lookup on the last iframe: %q", got)
	}
	w.noSlotsLeft()
}

func TestHeldLookupAfterHoldParksAsBefore(t *testing.T) {
	reply := hlChromeReply(68, "", hlFrame, hlFrameURL, hlSecret)
	want := hlReleasedBytes(t, 68, "", reply)
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	w.hold(hlFrame, hlFrameSess, true)
	l := w.lookup(68, "", hlFrame)
	if l.parked == nil {
		t.Fatal("a lookup after the hold began was not parked")
	}
	if !w.logged("holding Target.getTargetInfo until the guard decides an auto-attached iframe") {
		t.Fatalf("the existing park path changed: %q", w.logs)
	}
	w.learnURL(hlFrame, hlFrameURL)
	w.resume(l)
	if l.pv.forward == nil {
		t.Fatalf("the parked lookup was not forwarded after the release: %+v", l.pv)
	}
	v := w.browser(reply)
	if string(v.forward) != want {
		t.Fatalf("the parked lookup's answer changed: %s want %s", v.forward, want)
	}
	w.settleQuiet()
	if w.lateCount() != 0 {
		t.Fatalf("the existing park path answered through the async page path: %q", w.lateStrings())
	}
	w.noSlotsLeft()
}

func TestHeldLookupSameValue(t *testing.T) {
	w := hlNew(t, hlPageURL)
	w.announce(hlFrame)
	first := w.lookup(10, "", hlFrame)
	if first.pv.forward == nil {
		t.Fatal("lookup 10 not forwarded")
	}
	w.hold(hlFrame, hlFrameSess, true)
	second := w.lookup(11, "", hlFrame)
	if second.parked == nil {
		t.Fatal("lookup 11 after the hold was not parked")
	}
	w.browser(hlChromeReply(10, "", hlFrame, "", hlSecret))
	w.mustUnanswered(10, "", "while its iframe was held")
	w.learnURL(hlFrame, hlFrameURL)
	w.waitLate(1)
	w.resume(second)
	w.browser(hlChromeReply(11, "", hlFrame, "", hlSecret))
	third := w.lookup(12, "", hlFrame)
	if third.parked != nil || third.pv.forward == nil {
		t.Fatal("lookup 12 after the release was not forwarded")
	}
	w.browser(hlChromeReply(12, "", hlFrame, "", hlSecret))
	w.settleQuiet()
	norm := func(id int64) string {
		got := w.answers(id, "")
		if len(got) != 1 {
			t.Fatalf("lookup %d answers %q", id, got)
		}
		return strings.Replace(got[0], fmt.Sprintf(`"id":%d`, id), `"id":0`, 1)
	}
	a, b, c := norm(10), norm(11), norm(12)
	if a != b || b != c || !strings.Contains(a, hlSecret) {
		t.Fatalf("repeated lookups disagree:\n%s\n%s\n%s", a, b, c)
	}
	if w.lateCount() != 1 {
		t.Fatalf("want one answer through the async page path, got %q", w.lateStrings())
	}
	w.noSlotsLeft()
}

func TestHeldLookupStockTranscriptUnchanged(t *testing.T) {
	for _, row := range []struct {
		name    string
		prepare func(w *hlWire)
		want    string
	}{
		{"attached-with-url", func(w *hlWire) {
			w.relay("Target.targetCreated", hlFrameInfo(hlFrame, hlFrameURL, ""))
			w.root("Target.targetCreated", hlFrameInfo(hlFrame, hlFrameURL, ""))
			w.browser(encode(map[string]any{"method": "Target.attachedToTarget", "sessionId": hlPageSession, "params": map[string]any{"sessionId": hlFrameSess, "waitingForDebugger": true, "targetInfo": hlFrameInfo(hlFrame, hlFrameURL, "")}}))
		}, hlStockAttached},
		{"announced-never-held", func(w *hlWire) {
			w.announce(hlFrame)
		}, hlStockAnnounced},
		{"released-before-reply", func(w *hlWire) {
			w.announce(hlFrame)
			w.hold(hlFrame, hlFrameSess, true)
			w.learnURL(hlFrame, hlFrameURL)
		}, hlStockReleased},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := hlNew(t, hlPageURL)
			row.prepare(w)
			w.mu.Lock()
			clientBefore, chromeBefore := len(w.client), len(w.chrome)
			w.mu.Unlock()
			l := w.lookup(67, "", hlFrame)
			if l.parked != nil || l.pv.forward == nil || l.pv.reply != nil || len(l.pv.send) != 0 {
				t.Fatalf("the lookup was not forwarded as is: %+v", l.pv)
			}
			if string(l.pv.forward) != `{"id":67,"method":"Target.getTargetInfo","params":{"targetId":"HL-FRAME"}}` {
				t.Fatalf("the lookup reached Chrome changed: %s", l.pv.forward)
			}
			v := w.g.fromBrowser(hlChromeReply(67, "", hlFrame, hlFrameURL, hlSecret))
			if string(v.forward) != row.want || len(v.page) != 0 || len(v.send) != 0 {
				t.Fatalf("stock transcript changed:\nforward=%s\nwant   =%s\npage=%q send=%q", v.forward, row.want, v.page, v.send)
			}
			w.settleQuiet()
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.late) != 0 || len(w.client) != clientBefore || len(w.chrome) != chromeBefore+1 {
				t.Fatalf("extra messages around a stock lookup: late=%q client=%d/%d chrome=%d/%d", w.late, len(w.client), clientBefore, len(w.chrome), chromeBefore+1)
			}
		})
	}
}

const (
	hlStockAttached  = `{"id":67,"result":{"targetInfo":{"attached":true,"browserContextId":"CTX","canAccessOpener":false,"parentFrameId":"HL-PAGE","targetId":"HL-FRAME","title":"HL-UNCLASSIFIED-TITLE","type":"iframe","url":"https://frame.other.test/inner?in=framed"}}}`
	hlStockAnnounced = hlStockAttached
	hlStockReleased  = hlStockAttached
)
