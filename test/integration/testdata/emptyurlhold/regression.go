package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func emptyURLHoldGuard(t *testing.T) *guard {
	t.Helper()
	return emptyURLHoldGuardLog(t, nil)
}

func emptyURLHoldGuardLog(t *testing.T, logf func(string, ...any)) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, logf)
	g.relaySession = "RELAY"
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-OPENER"] = &session{id: "S-OPENER", target: "OPENER", typ: "page"}
	g.targets["SHARED"] = &target{id: "SHARED", typ: "page", url: "https://ordinary.test/shared"}
	return g
}

func emptyURLHoldPayloads(v browserVerdict) []string {
	var out []string
	if v.forward != nil {
		out = append(out, string(v.forward))
	}
	for _, b := range v.page {
		out = append(out, string(b))
	}
	return out
}

func emptyURLHoldContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func emptyURLHoldAttachIndex(ss []string, sessionID string) int {
	needle := `"params":{"sessionId":"` + sessionID + `"`
	for i, s := range ss {
		if strings.Contains(s, "attachedToTarget") && strings.Contains(s, needle) {
			return i
		}
	}
	return -1
}

func emptyURLHoldAttachCount(ss []string, sessionID string) int {
	n := 0
	needle := `"params":{"sessionId":"` + sessionID + `"`
	for _, s := range ss {
		if strings.Contains(s, "attachedToTarget") && strings.Contains(s, needle) {
			n++
		}
	}
	return n
}

func emptyURLTabAttach(waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-TAB",
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": "TAB", "type": "tab", "url": "", "openerId": "SHARED"},
		},
	})
}

func emptyURLPageAttach(waiting bool) []byte {
	return encode(map[string]any{
		"sessionId": "S-TAB",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-PAGE",
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": "PAGE", "type": "page", "url": "", "openerId": "SHARED"},
		},
	})
}

func emptyURLPageAttachFirst(waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-PAGE",
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": "PAGE", "type": "page", "url": "", "openerId": "SHARED"},
		},
	})
}

func emptyURLRelayChanged(targetID, typ, url string) []byte {
	return encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": targetID, "type": typ, "url": url, "openerId": "SHARED"},
		},
	})
}

func emptyURLAttachPair(t *testing.T, g *guard, tabFirst bool) {
	t.Helper()
	if tabFirst {
		_ = g.fromBrowser(emptyURLTabAttach(true))
		_ = g.fromBrowser(emptyURLPageAttach(true))
		return
	}
	_ = g.fromBrowser(emptyURLPageAttachFirst(true))
	_ = g.fromBrowser(emptyURLTabAttach(true))
}

func TestEmptyURLHoldAnnounceEmptyTabHoldOnlyPage(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLTabAttach(true))
	client := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(client, "S-TAB") < 0 || g.sessions["S-TAB"] == nil {
		t.Fatalf("empty-URL tab must be announced like main so the client can arm setAutoAttach: %v", client)
	}
	if g.held["S-TAB"] != nil {
		t.Fatal("empty-URL tab must not be held")
	}
	v = g.fromBrowser(emptyURLPageAttach(true))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("empty-URL page child announced before classification: %v", ps)
	}
	if g.held["S-PAGE"] == nil {
		t.Fatal("empty-URL page child was not held")
	}
	if !g.held["S-PAGE"].resumed {
		t.Fatal("held empty-URL page was not guard-resumed")
	}
}

func TestEmptyURLHoldTabPopupPageChildResumedAtHold(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		waiting := waiting
		t.Run(fmt.Sprintf("waiting=%t", waiting), func(t *testing.T) {
			g := emptyURLHoldGuard(t)
			_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
			if g.sessions["S-TAB-POP"] == nil || g.held["S-TAB-POP"] != nil {
				t.Fatal("popup tab must be announced so the client can arm tab-level setAutoAttach")
			}
			v := g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", waiting))
			ps := emptyURLHoldPayloads(v)
			if emptyURLHoldContains(ps, "attachedToTarget") {
				t.Fatalf("empty-URL popup page child announced before classification: %v", ps)
			}
			h := g.held["S-PAGE-POP"]
			if h == nil {
				t.Fatal("empty-URL popup page child was not held")
			}
			if !h.resumed {
				t.Fatalf("held popup page child must be guard-resumed at hold time waiting=%t", waiting)
			}
			sends := emptyURLHoldSends(v)
			if n := emptyURLHoldResumeCount(sends, "S-PAGE-POP"); n != 1 {
				t.Fatalf("hold must send Runtime.runIfWaitingForDebugger exactly once on the held page child waiting=%t count=%d sends=%v", waiting, n, sends)
			}
		})
	}
}

func emptyURLHoldResumeCount(ss []string, sessionID string) int {
	n := 0
	needle := `"sessionId":"` + sessionID + `"`
	for _, s := range ss {
		if strings.Contains(s, "Runtime.runIfWaitingForDebugger") && strings.Contains(s, needle) {
			n++
		}
	}
	return n
}

func emptyURLReview(g *guard, raw string) pageVerdict {
	c, id, ok := parseCommand([]byte(raw))
	return g.fromCommand(c, id, ok)
}

func TestEmptyURLHoldResumedMapBoundedAndAckAfterPrune(t *testing.T) {
	g := emptyURLHoldGuard(t)
	limit := holdResumeCapLimit()
	if !holdResumeCapEnabled() || limit <= 0 {
		t.Fatal("named row requires a positive holdResumed cap")
	}
	for i := 0; i < 6; i++ {
		tab := fmt.Sprintf("S-TAB-E%d", i)
		page := fmt.Sprintf("S-PAGE-E%d", i)
		_ = g.fromBrowser(emptyURLNamedTabAttach(tab, fmt.Sprintf("TAB-E%d", i), "OPENER", true))
		_ = g.fromBrowser(emptyURLNamedPageAttach(tab, page, fmt.Sprintf("PAGE-E%d", i), "OPENER", false))
		if g.held[page] == nil || !g.holdResumed[page] {
			t.Fatalf("empty-URL hold %s must be resumed into holdResumed", page)
		}
	}
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-KEEP", "TAB-KEEP", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-KEEP", "S-PAGE-KEEP", "PAGE-KEEP", "OPENER", false))
	_ = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-KEEP", "page", "https://ordinary.test/keep", "OPENER"))
	if g.sessions["S-PAGE-KEEP"] == nil || g.held["S-PAGE-KEEP"] != nil || !g.holdResumed["S-PAGE-KEEP"] {
		t.Fatal("expected delivered keep page still marked holdResumed")
	}
	g.mu.Lock()
	for i := 0; i < limit+3; i++ {
		g.noteHoldResumedLocked(fmt.Sprintf("S-PAD-%d", i))
	}
	if len(g.holdResumed) > limit || len(g.holdResumedOrder) > limit {
		g.mu.Unlock()
		t.Fatalf("holdResumed grew past cap: map=%d order=%d limit=%d", len(g.holdResumed), len(g.holdResumedOrder), limit)
	}
	latest := fmt.Sprintf("S-PAD-%d", limit+2)
	if !g.holdResumed[latest] {
		g.mu.Unlock()
		t.Fatal("newest holdResumed entry must survive prune")
	}
	if g.holdResumed["S-PAGE-KEEP"] || g.holdResumed["S-PAGE-E0"] {
		g.mu.Unlock()
		t.Fatal("oldest holdResumed entries must be pruned once the cap is exceeded")
	}
	g.mu.Unlock()
	ack := emptyURLReview(g, fmt.Sprintf(`{"id":80,"sessionId":"%s","method":"Runtime.runIfWaitingForDebugger","params":{}}`, latest))
	if len(ack.forward) > 0 {
		t.Fatalf("client resume after prune must ack without a second guard resume: %s", ack.forward)
	}
	if len(ack.reply) == 0 || strings.Contains(string(ack.reply), "Session with given id not found") {
		t.Fatalf("client resume after prune must ack: %s", ack.reply)
	}
	g.mu.Lock()
	g.forgetSessionLocked(latest)
	if g.holdResumed[latest] {
		g.mu.Unlock()
		t.Fatal("forgetSessionLocked must delete the holdResumed entry")
	}
	g.mu.Unlock()
}

func TestEmptyURLHoldClientResumeAfterGuardResumeIsIdempotent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	v := g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	if g.held["S-PAGE-POP"] == nil || !g.held["S-PAGE-POP"].resumed {
		t.Fatal("expected held resumed page child")
	}
	heldAck := emptyURLReview(g, `{"id":20,"sessionId":"S-PAGE-POP","method":"Runtime.runIfWaitingForDebugger","params":{}}`)
	if len(heldAck.forward) > 0 {
		t.Fatalf("client resume while held was forwarded: %s", heldAck.forward)
	}
	if len(heldAck.reply) == 0 || strings.Contains(string(heldAck.reply), "Session with given id not found") {
		t.Fatalf("client resume while held must ack without missing session: %s", heldAck.reply)
	}
	_ = g.fromBrowser(emptyURLRelayChanged("PAGE-POP", "page", "https://ordinary.test/popup"))
	if g.held["S-PAGE-POP"] != nil || g.sessions["S-PAGE-POP"] == nil {
		t.Fatal("ordinary classification must deliver the page child")
	}
	after := emptyURLReview(g, `{"id":21,"sessionId":"S-PAGE-POP","method":"Runtime.runIfWaitingForDebugger","params":{}}`)
	if len(after.forward) > 0 {
		t.Fatalf("client resume after deliver was a second guard resume: %s", after.forward)
	}
	if len(after.reply) == 0 || strings.Contains(string(after.reply), "Session with given id not found") {
		t.Fatalf("client resume after deliver must ack without missing session: %s", after.reply)
	}
	_ = emptyURLHoldSends(v)
}

func TestEmptyURLHoldClientResumeAfterExpiryIsIdempotent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	_, _ = g.expireHold("S-PAGE-POP")
	v := emptyURLReview(g, `{"id":22,"sessionId":"S-PAGE-POP","method":"Runtime.runIfWaitingForDebugger","params":{}}`)
	if len(v.forward) > 0 {
		t.Fatalf("client resume after expiry was forwarded: %s", v.forward)
	}
	if len(v.reply) == 0 || strings.Contains(string(v.reply), "Session with given id not found") {
		t.Fatalf("client resume after expiry must ack without missing session: %s", v.reply)
	}
}

func TestEmptyURLHoldClientResumeAfterRefuseIsIdempotent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	_ = g.fromBrowser(emptyURLRelayChanged("PAGE-POP", "page", "https://seven.sliccy.ai/popup"))
	v := emptyURLReview(g, `{"id":23,"sessionId":"S-PAGE-POP","method":"Runtime.runIfWaitingForDebugger","params":{}}`)
	if len(v.forward) > 0 {
		t.Fatalf("client resume after refuse was forwarded: %s", v.forward)
	}
	if len(v.reply) == 0 || strings.Contains(string(v.reply), "Session with given id not found") {
		t.Fatalf("client resume after refuse must ack without missing session: %s", v.reply)
	}
}

func TestEmptyURLHoldResumeUnheldChild(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	v := g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	if g.held["S-PAGE-POP"] == nil {
		t.Fatal("named row: page child must stay held and tracked")
	}
	if emptyURLHoldResumeCount(emptyURLHoldSends(v), "S-PAGE-POP") != 1 {
		t.Fatal("named row: held child must be resumed exactly once")
	}
}

func TestEmptyURLHoldResumeTwice(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	v := g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	if n := emptyURLHoldResumeCount(emptyURLHoldSends(v), "S-PAGE-POP"); n != 1 {
		t.Fatalf("named row: resume exactly once, got %d", n)
	}
}

func TestEmptyURLHoldResumeRefusedChild(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	hold := g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	before := emptyURLHoldResumeCount(emptyURLHoldSends(hold), "S-PAGE-POP")
	v := g.fromBrowser(emptyURLRelayChanged("PAGE-POP", "page", "https://seven.sliccy.ai/popup"))
	after := emptyURLHoldResumeCount(emptyURLHoldSends(v), "S-PAGE-POP")
	if before != 1 || after != 0 {
		t.Fatalf("named row: refused child must not get another guard resume (hold=%d refuse=%d)", before, after)
	}
	if g.sessions["S-PAGE-POP"] != nil || emptyURLHoldContains(emptyURLHoldPayloads(v), "attachedToTarget") {
		t.Fatal("named row: refused child must stay detached from the client")
	}
}

func TestEmptyURLHoldResumeProtectedFateUnchanged(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-POP", "TAB-POP", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-POP", "S-PAGE-POP", "PAGE-POP", "OPENER", false))
	if g.held["S-PAGE-POP"] == nil || !g.held["S-PAGE-POP"].resumed {
		t.Fatal("expected resumed held page before protected classification")
	}
	v := g.fromBrowser(emptyURLRelayChanged("PAGE-POP", "page", "https://seven.sliccy.ai/popup"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("named row: protected fate must stay detached (no client attach): %v", ps)
	}
	if g.sessions["S-PAGE-POP"] != nil || g.held["S-PAGE-POP"] != nil {
		t.Fatal("named row: protected fate must latch/detach the page child")
	}
	if g.sessions["S-TAB-POP"] != nil {
		t.Fatal("named row: protected fate must detach the announced tab")
	}
}

func TestEmptyURLHoldOrdinaryAnnouncesTabThenPage(t *testing.T) {
	g := emptyURLHoldGuard(t)
	tabClient := emptyURLHoldPayloads(g.fromBrowser(emptyURLTabAttach(true)))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	if g.sessions["S-TAB"] == nil || g.held["S-PAGE"] == nil {
		t.Fatal("expected announced tab and held page before classification")
	}
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://ordinary.test/popup"))
	ps := append(tabClient, emptyURLHoldPayloads(v)...)
	tabAt := emptyURLHoldAttachIndex(ps, "S-TAB")
	pageAt := emptyURLHoldAttachIndex(ps, "S-PAGE")
	if tabAt < 0 || pageAt < 0 {
		t.Fatalf("ordinary classification missing tab/page attach: %v", ps)
	}
	if tabAt > pageAt {
		t.Fatalf("ordinary classification announced page before tab: %v", ps)
	}
	if g.held["S-PAGE"] != nil {
		t.Fatal("ordinary classification left the page held")
	}
	if g.sessions["S-TAB"] == nil || g.sessions["S-PAGE"] == nil {
		t.Fatal("ordinary classification did not leave both sessions admitted")
	}
}

func TestEmptyURLHoldProtectedDetachesAnnouncedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	if g.sessions["S-TAB"] == nil {
		t.Fatal("expected announced tab before protected classification")
	}
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://seven.sliccy.ai/popup"))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("protected classification announced an attach: %v", ps)
	}
	if g.sessions["S-PAGE"] != nil {
		t.Fatal("protected classification admitted the page session")
	}
	if g.held["S-PAGE"] != nil {
		t.Fatal("protected classification left the page held")
	}
	if g.sessions["S-TAB"] != nil || emptyURLHoldDetachIndex(sends, "S-TAB") < 0 {
		t.Fatalf("protected child left a dangling announced tab: sessions=%v sends=%v", g.sessions["S-TAB"] != nil, sends)
	}
}

func TestEmptyURLHoldExpiryDetachesAnnouncedTabWithoutProtectedLabel(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	if g.sessions["S-TAB"] == nil || g.held["S-PAGE"] == nil {
		t.Fatal("expected announced tab and held page before expiry")
	}
	_, expSend := g.expireHold("S-PAGE")
	sends := emptyURLHoldSends(browserVerdict{send: expSend})
	if g.sessions["S-TAB"] != nil || g.sessions["S-PAGE"] != nil {
		t.Fatal("expiry left a dangling client session")
	}
	if g.held["S-PAGE"] != nil {
		t.Fatal("expiry left the page held")
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB") < 0 && !g.hidden["S-TAB"] {
		t.Fatalf("expiry left the announced tab without a matching detach: %v", sends)
	}
	for _, line := range logs {
		if strings.Contains(line, "protected origin") {
			t.Fatalf("unclassified expiry used the protected-origin label: %s", line)
		}
	}
}

func TestEmptyURLHoldPageBeforeTabOrdinaryOrder(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLPageAttachFirst(true))
	tabClient := emptyURLHoldPayloads(g.fromBrowser(emptyURLTabAttach(true)))
	if g.sessions["S-TAB"] == nil || g.held["S-PAGE"] == nil {
		t.Fatal("expected announced tab and held page in page-before-tab order")
	}
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://ordinary.test/popup"))
	ps := append(tabClient, emptyURLHoldPayloads(v)...)
	tabAt := emptyURLHoldAttachIndex(ps, "S-TAB")
	pageAt := emptyURLHoldAttachIndex(ps, "S-PAGE")
	if tabAt < 0 || pageAt < 0 {
		t.Fatalf("page-before-tab ordinary missing attaches: %v", ps)
	}
	if tabAt > pageAt {
		t.Fatalf("page-before-tab ordinary announced page before tab: %v", ps)
	}
}

func TestEmptyURLHoldDetachRacingVerdictLeavesNoDangle(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-PAGE", "targetId": "PAGE"},
	}))
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://ordinary.test/popup"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE") >= 0 {
		t.Fatalf("detach-racing verdict re-announced page: %v", ps)
	}
	if g.sessions["S-PAGE"] != nil {
		t.Fatal("detach-racing verdict left a dangling page session")
	}
}

func TestEmptyURLHoldDropBeforeDeliverGuardSession(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	nt := &navTarget{target: "TAB", session: "GUARD", ready: make(chan struct{})}
	g.navTargets["TAB"] = nt
	g.navSessions["GUARD"] = nt
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-LEAK",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "LEAK", "type": "tab", "url": "", "openerId": "SHARED"},
		},
	}))
	if len(emptyURLHoldPayloads(v)) > 0 {
		t.Fatalf("guard-session empty-URL attach reached the client: %v", emptyURLHoldPayloads(v))
	}
	v = g.fromBrowser(encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-LEAK2",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "LEAK2", "type": "tab", "url": "", "openerId": "SHARED"},
		},
	}))
	if len(emptyURLHoldPayloads(v)) > 0 {
		t.Fatalf("relay-session empty-URL attach reached the client: %v", emptyURLHoldPayloads(v))
	}
}

func TestEmptyURLHoldDropBeforeDeliverBlockedSession(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	g.held["S-BLOCK"] = &heldSession{id: "S-BLOCK", target: "BLOCK", typ: "page"}
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-BLOCK",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-CHILD",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "CHILD", "type": "page", "url": "", "openerId": "SHARED"},
		},
	}))
	if len(emptyURLHoldPayloads(v)) > 0 {
		t.Fatalf("held-session empty-URL attach reached the client: %v", emptyURLHoldPayloads(v))
	}
}

func TestEmptyURLHoldMutationHoldEmptyTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLTabAttach(true))
	if g.held["S-TAB"] != nil || g.sessions["S-TAB"] == nil || emptyURLHoldAttachIndex(emptyURLHoldPayloads(v), "S-TAB") < 0 {
		t.Fatal("mutation surface: empty-URL tab was held instead of announced")
	}
}

func TestEmptyURLHoldMutationGuardResumeSkipped(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	if g.held["S-TAB"] != nil {
		t.Fatal("mutation surface: empty-URL tab was held")
	}
	h := g.held["S-PAGE"]
	if h == nil {
		t.Fatal("expected page held")
	}
	if !h.resumed {
		t.Fatal("mutation surface: page was not guard-resumed")
	}
}

func TestEmptyURLHoldMutationExpiryLabelledProtected(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	_ = g.fromBrowser(emptyURLPageAttachFirst(true))
	if g.held["S-PAGE"] == nil {
		t.Fatal("expected page held")
	}
	_, _ = g.expireHold("S-PAGE")
	for _, line := range logs {
		if strings.Contains(line, "protected origin") {
			t.Fatalf("mutation surface: unclassified expiry used protected-origin wording: %s", line)
		}
	}
}

func emptyURLHoldSends(v browserVerdict) []string {
	var out []string
	for _, b := range v.send {
		out = append(out, string(b))
	}
	return out
}

func emptyURLHoldDetachIndex(ss []string, sessionID string) int {
	needle := `"sessionId":"` + sessionID + `"`
	for i, s := range ss {
		if strings.Contains(s, "detachFromTarget") && strings.Contains(s, needle) {
			return i
		}
	}
	return -1
}

func emptyURLNamedTabAttach(session, target, opener string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": target, "type": "tab", "url": "", "openerId": opener},
		},
	})
}

func emptyURLNamedPageAttach(parentSession, session, target, opener string, waiting bool) []byte {
	return encode(map[string]any{
		"sessionId": parentSession,
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": target, "type": "page", "url": "", "openerId": opener},
		},
	})
}

func emptyURLNamedPageAttachFirst(session, target, opener string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": target, "type": "page", "url": "", "openerId": opener},
		},
	})
}

func emptyURLNamedRelayChanged(target, typ, url, opener string) []byte {
	return encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": target, "type": typ, "url": url, "openerId": opener},
		},
	})
}

func emptyURLAttachNamedPair(t *testing.T, g *guard, tabSession, pageSession, tabID, pageID, opener string, tabFirst bool) {
	t.Helper()
	if tabFirst {
		_ = g.fromBrowser(emptyURLNamedTabAttach(tabSession, tabID, opener, true))
		_ = g.fromBrowser(emptyURLNamedPageAttach(tabSession, pageSession, pageID, opener, true))
		return
	}
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst(pageSession, pageID, opener, true))
	_ = g.fromBrowser(emptyURLNamedTabAttach(tabSession, tabID, opener, true))
}

func emptyURLAttachSiblingPairs(t *testing.T, g *guard, tabFirst bool) {
	t.Helper()
	emptyURLAttachNamedPair(t, g, "S-TAB-A", "S-PAGE-A", "TAB-A", "PAGE-A", "SHARED", tabFirst)
	emptyURLAttachNamedPair(t, g, "S-TAB-B", "S-PAGE-B", "TAB-B", "PAGE-B", "SHARED", tabFirst)
	for _, tab := range []string{"S-TAB-A", "S-TAB-B"} {
		if g.sessions[tab] == nil || g.held[tab] != nil {
			t.Fatalf("expected %s announced (not held) before classification", tab)
		}
	}
	for _, page := range []string{"S-PAGE-A", "S-PAGE-B"} {
		if g.held[page] == nil {
			t.Fatalf("expected %s held before classification", page)
		}
		if g.sessions[page] != nil {
			t.Fatalf("%s admitted before classification", page)
		}
	}
}

func emptyURLAssertPairStillHeld(t *testing.T, g *guard, tabSession, pageSession string, payloads []string) {
	t.Helper()
	if g.sessions[tabSession] == nil {
		t.Fatalf("expected %s still announced", tabSession)
	}
	if g.held[pageSession] == nil {
		t.Fatalf("expected %s still held", pageSession)
	}
	if g.sessions[pageSession] != nil {
		t.Fatalf("%s admitted while still unclassified", pageSession)
	}
	if emptyURLHoldAttachIndex(payloads, pageSession) >= 0 {
		t.Fatalf("unclassified sibling page reached the client: %v", payloads)
	}
}

func TestEmptyURLHoldSiblingOpenerOrdinaryLeavesOtherHeldThenProtected(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	emptyURLAttachSiblingPairs(t, g, true)
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://ordinary.test/a", "SHARED"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-A") < 0 || g.sessions["S-PAGE-A"] == nil {
		t.Fatalf("A ordinary missing page release: %v", ps)
	}
	if g.sessions["S-TAB-A"] == nil {
		t.Fatal("A ordinary lost announced tab A")
	}
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", ps)
	logs = nil
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://seven.sliccy.ai/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("B protected announced an attach: %v", ps)
	}
	if g.sessions["S-TAB-B"] != nil || g.sessions["S-PAGE-B"] != nil {
		t.Fatal("B protected admitted a client session")
	}
	if g.held["S-PAGE-B"] != nil {
		t.Fatal("B protected left the page held")
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 {
		t.Fatalf("B protected missing tab detach: %v", sends)
	}
	if g.sessions["S-TAB-A"] == nil || g.sessions["S-PAGE-A"] == nil {
		t.Fatal("B protected disturbed already-released A")
	}
	for _, line := range logs {
		if strings.Contains(line, "S-TAB-A") || strings.Contains(line, "S-PAGE-A") || strings.Contains(line, "TAB-A") || strings.Contains(line, "PAGE-A") {
			if strings.Contains(line, "detached") {
				t.Fatalf("B protected detached A: %s", line)
			}
		}
	}
}

func TestEmptyURLHoldSiblingOpenerOrdinaryLeavesOtherHeldThenOrdinary(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, true)
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://ordinary.test/a", "SHARED"))
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", emptyURLHoldPayloads(v))
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://ordinary.test/b", "SHARED"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-B") < 0 || emptyURLHoldAttachCount(ps, "S-PAGE-B") != 1 {
		t.Fatalf("B ordinary missing single page release: %v", ps)
	}
	if g.held["S-PAGE-B"] != nil {
		t.Fatal("B ordinary left the page held")
	}
	if g.sessions["S-TAB-B"] == nil || g.sessions["S-PAGE-B"] == nil {
		t.Fatal("B ordinary did not leave both sessions admitted")
	}
	if g.sessions["S-TAB-A"] == nil || g.sessions["S-PAGE-A"] == nil {
		t.Fatal("B ordinary disturbed already-released A")
	}
}

func TestEmptyURLHoldSiblingOpenerProtectedLeavesOtherHeldBothOutcomes(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, true)
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://seven.sliccy.ai/a", "SHARED"))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("A protected announced an attach: %v", ps)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-A") < 0 {
		t.Fatalf("A protected missing A tab detach: %v", sends)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") >= 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-B") >= 0 {
		t.Fatalf("A protected detached unclassified B: %v", sends)
	}
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", append(ps, sends...))
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://ordinary.test/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-B") < 0 || g.sessions["S-PAGE-B"] == nil || g.sessions["S-TAB-B"] == nil {
		t.Fatalf("B ordinary after A protected missing page release: %v", ps)
	}

	g2 := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g2, true)
	_ = g2.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://seven.sliccy.ai/a", "SHARED"))
	emptyURLAssertPairStillHeld(t, g2, "S-TAB-B", "S-PAGE-B", nil)
	v = g2.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://seven.sliccy.ai/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	sends = emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("B protected after A protected announced an attach: %v", ps)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 {
		t.Fatalf("B protected after A protected missing B tab detach: %v", sends)
	}
}

func TestEmptyURLHoldSiblingOpenerExpiryIndependent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, true)
	_, expSend := g.expireHold("S-PAGE-A")
	sends := emptyURLHoldSends(browserVerdict{send: expSend})
	if g.held["S-PAGE-A"] != nil {
		t.Fatal("expiry of A left page A held")
	}
	if g.sessions["S-TAB-A"] != nil || g.sessions["S-PAGE-A"] != nil {
		t.Fatal("expiry of A left a dangling A session")
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-A") < 0 && !g.hidden["S-TAB-A"] {
		t.Fatalf("expiry of A missing tab detach: %v", sends)
	}
	if g.held["S-PAGE-B"] == nil || g.sessions["S-TAB-B"] == nil {
		t.Fatal("expiry of A disturbed B before B's own HoldTimeout")
	}
	if g.sessions["S-PAGE-B"] != nil {
		t.Fatal("expiry of A admitted page B")
	}
	_, expSend = g.expireHold("S-PAGE-B")
	sends = emptyURLHoldSends(browserVerdict{send: expSend})
	if g.held["S-PAGE-B"] != nil || g.sessions["S-TAB-B"] != nil || g.sessions["S-PAGE-B"] != nil {
		t.Fatal("expiry of B left B behind")
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 && !g.hidden["S-TAB-B"] {
		t.Fatalf("expiry of B missing tab detach: %v", sends)
	}
}

func TestEmptyURLHoldSiblingOpenerPageBeforeTabIndependent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, false)
	if g.held["S-PAGE-A"].parent != "" || g.held["S-PAGE-B"].parent != "" {
		t.Fatalf("page-before-tab adopt must keep flat detach parent empty: A=%q B=%q", g.held["S-PAGE-A"].parent, g.held["S-PAGE-B"].parent)
	}
	if g.held["S-PAGE-A"].adoptedTab != "S-TAB-A" || g.held["S-PAGE-B"].adoptedTab != "S-TAB-B" {
		t.Fatalf("page-before-tab must adopt each held page to its announced tab: A=%q B=%q", g.held["S-PAGE-A"].adoptedTab, g.held["S-PAGE-B"].adoptedTab)
	}
	if heldOpenerLocked(g.held["S-PAGE-A"]) != "SHARED" {
		t.Fatal("page-before-tab pair A missing shared opener link")
	}
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://ordinary.test/a", "SHARED"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-A") < 0 || g.sessions["S-PAGE-A"] == nil || g.sessions["S-TAB-A"] == nil {
		t.Fatalf("page-before-tab A ordinary missing release: %v", ps)
	}
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", ps)
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://seven.sliccy.ai/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("page-before-tab B protected announced an attach: %v", ps)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 {
		t.Fatalf("page-before-tab B protected missing tab detach: %v", sends)
	}
	if g.sessions["S-TAB-A"] == nil || g.sessions["S-PAGE-A"] == nil {
		t.Fatal("page-before-tab B protected disturbed A")
	}
}

func emptyURLNamedPageAttachURL(parentSession, session, target, opener, url string, waiting bool) []byte {
	ev := map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": target, "type": "page", "url": url, "openerId": opener},
		},
	}
	if parentSession != "" {
		ev["sessionId"] = parentSession
	}
	return encode(ev)
}

func emptyURLTargetCreated(target, typ, url, opener string) []byte {
	return encode(map[string]any{
		"method": "Target.targetCreated",
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": target, "type": typ, "url": url, "openerId": opener},
		},
	})
}

func emptyURLCreateTargetWire(t *testing.T, g *guard, pageURL string) (client []string) {
	t.Helper()
	pv := g.fromCommand(&command{id: 41, method: "Target.createTarget", params: map[string]any{"url": pageURL}}, 41, true)
	if len(pv.reply) > 0 {
		t.Fatalf("Target.createTarget refused: %s", pv.reply)
	}
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-CT", "TAB-CT", "", true))
	client = append(client, emptyURLHoldPayloads(v)...)
	if g.sessions["S-TAB-CT"] == nil || g.held["S-TAB-CT"] != nil {
		t.Fatal("createTarget tab must be announced so the client can arm setAutoAttach")
	}
	_ = g.fromBrowser(emptyURLTargetCreated("PAGE-CT", "page", pageURL, ""))
	_ = g.fromBrowser(encode(map[string]any{"id": 41, "result": map[string]any{"targetId": "PAGE-CT"}}))
	v = g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-CT", "S-PAGE-CT", "PAGE-CT", "", pageURL, false))
	client = append(client, emptyURLHoldPayloads(v)...)
	return client
}

func emptyURLAssertClientDrivesPage(t *testing.T, g *guard, client []string, tabSession, pageSession string) {
	t.Helper()
	if g.held[tabSession] != nil || g.held[pageSession] != nil {
		t.Fatalf("client-created tab/page still held: tab=%v page=%v", g.held[tabSession] != nil, g.held[pageSession] != nil)
	}
	if g.sessions[tabSession] == nil || g.sessions[pageSession] == nil {
		t.Fatalf("client-created tab/page not admitted: tab=%v page=%v client=%v", g.sessions[tabSession] != nil, g.sessions[pageSession] != nil, client)
	}
	tabAt := emptyURLHoldAttachIndex(client, tabSession)
	pageAt := emptyURLHoldAttachIndex(client, pageSession)
	if tabAt < 0 || pageAt < 0 {
		t.Fatalf("client never saw tab/page attach announces: %v", client)
	}
	if tabAt > pageAt {
		t.Fatalf("client saw page before tab: %v", client)
	}
}

func TestEmptyURLHoldCreateTargetURLTabNotDeadlocked(t *testing.T) {
	g := emptyURLHoldGuard(t)
	client := emptyURLCreateTargetWire(t, g, "https://ordinary.test/child")
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-CT", "S-PAGE-CT")
}

func TestEmptyURLHoldCreateTargetBlankThenNavigate(t *testing.T) {
	g := emptyURLHoldGuard(t)
	client := emptyURLCreateTargetWire(t, g, "about:blank")
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-CT", "S-PAGE-CT")
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-CT", "page", "https://ordinary.test/from-blank", ""))
	if g.sessions["S-PAGE-CT"] == nil || g.held["S-PAGE-CT"] != nil {
		t.Fatalf("blank-tab lost the page session after URL change: held=%v payloads=%v", g.held["S-PAGE-CT"] != nil, emptyURLHoldPayloads(v))
	}
}

func TestEmptyURLHoldWindowOpenFromAdmittedOrdinaryOpener(t *testing.T) {
	g := emptyURLHoldGuard(t)
	var client []string
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-WO", "TAB-WO", "OPENER", true))
	client = append(client, emptyURLHoldPayloads(v)...)
	v = g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-WO", "S-PAGE-WO", "PAGE-WO", "OPENER", "https://ordinary.test/popup", false))
	client = append(client, emptyURLHoldPayloads(v)...)
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-WO", "S-PAGE-WO")
}

func emptyURLAnnounceFlatTabs(t *testing.T, g *guard, n int) (client []string) {
	t.Helper()
	for i := 1; i <= n; i++ {
		tab := fmt.Sprintf("TAB-%d", i)
		sid := "S-" + tab
		v := g.fromBrowser(emptyURLNamedTabAttach(sid, tab, "", true))
		client = append(client, emptyURLHoldPayloads(v)...)
		if g.sessions[sid] == nil || g.held[sid] != nil {
			t.Fatalf("expected %s announced not held", sid)
		}
	}
	return client
}

func TestEmptyURLHoldFlatStockOrdinaryMatchesLoneUnmatchedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	client := emptyURLAnnounceFlatTabs(t, g, 1)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-1", "PAGE-1", "OPENER", "https://ordinary.test/popup", false))
	client = append(client, emptyURLHoldPayloads(v)...)
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-1", "S-PAGE-1")
}

func TestEmptyURLHoldFlatOrdinaryAmbiguousTwoUnmatchedDoesNotMatch(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, 2)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-1", "PAGE-1", "OPENER", "https://ordinary.test/p1", false))
	ps := emptyURLHoldPayloads(v)
	if g.sessions["S-PAGE-1"] == nil {
		t.Fatal("ordinary flat page should still classify")
	}
	if emptyURLHoldAttachIndex(ps, "S-TAB-1") >= 0 || emptyURLHoldAttachIndex(ps, "S-TAB-2") >= 0 {
		t.Fatalf("ambiguous ordinary must not match either unmatched tab by guess: %v", ps)
	}
	if g.sessions["S-TAB-1"] == nil || g.sessions["S-TAB-2"] == nil {
		t.Fatal("ambiguous ordinary must leave both announced tabs unmatched")
	}
}

func emptyURLAssertFlatProtectedDetachesAll(t *testing.T, n int) {
	t.Helper()
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, n)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-PAGE-P"] != nil {
		t.Fatalf("protected flat page reached the client: %v", ps)
	}
	for i := 1; i <= n; i++ {
		sid := fmt.Sprintf("S-TAB-%d", i)
		if g.sessions[sid] != nil || (emptyURLHoldDetachIndex(sends, sid) < 0 && !g.hidden[sid]) {
			t.Fatalf("protected flat page left unmatched %s dangling: sessions=%v sends=%v", sid, g.sessions[sid] != nil, sends)
		}
	}
}

func TestEmptyURLHoldFlatProtectedDetachesOneUnmatchedTab(t *testing.T) {
	emptyURLAssertFlatProtectedDetachesAll(t, 1)
}
func TestEmptyURLHoldFlatProtectedDetachesTwoUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatProtectedDetachesAll(t, 2)
}
func TestEmptyURLHoldFlatProtectedDetachesThreeUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatProtectedDetachesAll(t, 3)
}

func emptyURLAssertFlatExpiredDetachesAll(t *testing.T, n int) {
	t.Helper()
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	_ = emptyURLAnnounceFlatTabs(t, g, n)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-E", "PAGE-E", "OPENER", true))
	if g.held["S-PAGE-E"] == nil {
		t.Fatal("expected flat empty page held")
	}
	_, expSend := g.expireHold("S-PAGE-E")
	sends := emptyURLHoldSends(browserVerdict{send: expSend})
	for i := 1; i <= n; i++ {
		sid := fmt.Sprintf("S-TAB-%d", i)
		if g.sessions[sid] != nil || (emptyURLHoldDetachIndex(sends, sid) < 0 && !g.hidden[sid]) {
			t.Fatalf("expired flat page left unmatched %s dangling: sessions=%v sends=%v", sid, g.sessions[sid] != nil, sends)
		}
	}
	for _, line := range logs {
		if strings.Contains(line, "protected origin") {
			t.Fatalf("unclassified flat expiry used protected-origin wording: %s", line)
		}
	}
}

func TestEmptyURLHoldFlatExpiredDetachesOneUnmatchedTab(t *testing.T) {
	emptyURLAssertFlatExpiredDetachesAll(t, 1)
}
func TestEmptyURLHoldFlatExpiredDetachesTwoUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatExpiredDetachesAll(t, 2)
}
func TestEmptyURLHoldFlatExpiredDetachesThreeUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatExpiredDetachesAll(t, 3)
}

func TestEmptyURLHoldWindowOpenEmptyPageFromOrdinaryOpenerThenClassifies(t *testing.T) {
	g := emptyURLHoldGuard(t)
	tabClient := emptyURLHoldPayloads(g.fromBrowser(emptyURLNamedTabAttach("S-TAB-WO", "TAB-WO", "OPENER", true)))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-WO", "S-PAGE-WO", "PAGE-WO", "OPENER", true))
	if g.sessions["S-TAB-WO"] == nil || g.held["S-PAGE-WO"] == nil {
		t.Fatal("expected announced tab and held page before classification")
	}
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-WO", "page", "https://ordinary.test/popup", "OPENER"))
	emptyURLAssertClientDrivesPage(t, g, append(tabClient, emptyURLHoldPayloads(v)...), "S-TAB-WO", "S-PAGE-WO")
}

func emptyURLAssertNeverAnnounced(t *testing.T, payloads []string, sessions ...string) {
	t.Helper()
	for _, sid := range sessions {
		if emptyURLHoldAttachIndex(payloads, sid) >= 0 {
			t.Fatalf("%s reached the client: %v", sid, payloads)
		}
	}
}

func TestEmptyURLHoldProtectedOpenerPageHeld(t *testing.T) {
	g := emptyURLHoldGuard(t)
	g.targets["PROT"] = &target{id: "PROT", typ: "page", url: "https://seven.sliccy.ai/", protected: true}
	g.sessions["S-PROT"] = &session{id: "S-PROT", target: "PROT", typ: "page"}
	tabV := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-P", "TAB-P", "PROT", true))
	tabPS := emptyURLHoldPayloads(tabV)
	tabSends := emptyURLHoldSends(tabV)
	if g.sessions["S-TAB-P"] != nil || emptyURLHoldAttachIndex(tabPS, "S-TAB-P") >= 0 {
		t.Fatalf("protected-opener empty tab reached the client: sessions=%v payloads=%v", g.sessions["S-TAB-P"] != nil, tabPS)
	}
	if emptyURLHoldDetachIndex(tabSends, "S-TAB-P") < 0 && !g.hidden["S-TAB-P"] {
		t.Fatalf("protected-opener empty tab was not hidden like main: sends=%v", tabSends)
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-P", "S-PAGE-P", "PAGE-P", "PROT", "https://ordinary.test/from-prot", false))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-P") >= 0 || g.sessions["S-PAGE-P"] != nil {
		t.Fatalf("protected-opener page reached the client: %v", ps)
	}
}

func TestEmptyURLHoldNestedPageChildMatchesAnnouncedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	tabClient := emptyURLHoldPayloads(g.fromBrowser(emptyURLNamedTabAttach("S-TAB-OR", "TAB-OR", "", true)))
	if g.sessions["S-TAB-OR"] == nil {
		t.Fatal("expected announced tab before page child")
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-OR", "S-PAGE-OR", "PAGE-OR", "", "https://ordinary.test/order", false))
	client := append(tabClient, emptyURLHoldPayloads(v)...)
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-OR", "S-PAGE-OR")
}

func TestEmptyURLHoldOrdinaryOpenerTabAnnouncedPageHeld(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-WO", "TAB-WO", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-WO", "S-PAGE-WO", "PAGE-WO", "OPENER", true))
	if g.sessions["S-TAB-WO"] == nil {
		t.Fatal("ordinary-opener empty tab must be announced so the client can arm setAutoAttach")
	}
	if g.held["S-PAGE-WO"] == nil || g.sessions["S-PAGE-WO"] != nil {
		t.Fatal("ordinary-opener empty page must stay held until classified")
	}
}

func TestEmptyURLHoldOrdinaryOpenerTabDetachedWhenChildProtected(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-WO", "TAB-WO", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-WO", "S-PAGE-WO", "PAGE-WO", "OPENER", true))
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-WO", "page", "https://seven.sliccy.ai/popup", "OPENER"))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	emptyURLAssertNeverAnnounced(t, ps, "S-PAGE-WO")
	if g.sessions["S-PAGE-WO"] != nil {
		t.Fatal("ordinary-opener protected child left a page session")
	}
	if g.sessions["S-TAB-WO"] != nil || emptyURLHoldDetachIndex(sends, "S-TAB-WO") < 0 {
		t.Fatalf("ordinary-opener protected child left a dangling announced tab: sessions=%v sends=%v", g.sessions["S-TAB-WO"] != nil, sends)
	}
	found := false
	for _, line := range logs {
		if strings.Contains(line, "protected origin") && (strings.Contains(line, "S-PAGE-WO") || strings.Contains(line, "PAGE-WO") || strings.Contains(line, "S-TAB-WO") || strings.Contains(line, "TAB-WO")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ordinary-opener protected detach missing protected-origin label: %v", logs)
	}
}

func emptyURLNamedTabAttachURL(session, target, opener, url string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          session,
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": target, "type": "tab", "url": url, "openerId": opener},
		},
	})
}

func emptyURLHoldDetachEnvelope(ss []string, targetSession, envelopeSession string) int {
	for i, s := range ss {
		if !strings.Contains(s, "detachFromTarget") {
			continue
		}
		var m struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Params    struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(s), &m); err != nil || m.Method != "Target.detachFromTarget" {
			continue
		}
		if m.Params.SessionID == targetSession && m.SessionID == envelopeSession {
			return i
		}
	}
	return -1
}

func TestEmptyURLHoldURLTabsSurviveFlatProtectedHide(t *testing.T) {
	g := emptyURLHoldGuard(t)
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("S-TAB-U%d", i)
		tid := fmt.Sprintf("TAB-U%d", i)
		_ = g.fromBrowser(emptyURLNamedTabAttachURL(sid, tid, "", fmt.Sprintf("https://ordinary.test/t%d", i), false))
		if g.sessions[sid] == nil {
			t.Fatalf("expected %s announced with a URL", sid)
		}
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-PAGE-P"] != nil {
		t.Fatalf("protected flat page reached the client: %v", ps)
	}
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("S-TAB-U%d", i)
		if g.sessions[sid] == nil {
			t.Fatalf("flat protected hide detached unrelated URL tab %s", sid)
		}
		if emptyURLHoldDetachIndex(sends, sid) >= 0 {
			t.Fatalf("flat protected hide detached unrelated URL tab %s: %v", sid, sends)
		}
	}
}

func TestEmptyURLHoldOnlyEmptyURLTabIsFlatHideCandidate(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttachURL("S-TAB-URL", "TAB-URL", "", "https://ordinary.test/known", false))
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-EMPTY", "TAB-EMPTY", "", true))
	if g.sessions["S-TAB-URL"] == nil || g.sessions["S-TAB-EMPTY"] == nil {
		t.Fatal("expected both tabs announced before flat hide")
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	sends := emptyURLHoldSends(v)
	if g.sessions["S-TAB-URL"] == nil || emptyURLHoldDetachIndex(sends, "S-TAB-URL") >= 0 {
		t.Fatalf("URL tab must never be a flat-hide candidate: sessions=%v sends=%v", g.sessions["S-TAB-URL"] != nil, sends)
	}
	if g.sessions["S-TAB-EMPTY"] != nil || (emptyURLHoldDetachIndex(sends, "S-TAB-EMPTY") < 0 && !g.hidden["S-TAB-EMPTY"]) {
		t.Fatalf("still-empty unmatched tab must be detached on flat hide: sessions=%v sends=%v", g.sessions["S-TAB-EMPTY"] != nil, sends)
	}
}

func TestEmptyURLHoldStartupProtectedPageLeavesOrdinaryTabs(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttachURL("S-TAB-1", "TAB-1", "", "https://ordinary.test/one", false))
	_ = g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-1", "PAGE-1", "", "https://ordinary.test/one", false))
	_ = g.fromBrowser(emptyURLNamedTabAttachURL("S-TAB-2", "TAB-2", "", "https://ordinary.test/two", false))
	_ = g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-2", "PAGE-2", "", "https://ordinary.test/two", false))
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "", "https://seven.sliccy.ai/startup", false))
	sends := emptyURLHoldSends(v)
	if g.sessions["S-PAGE-P"] != nil {
		t.Fatal("startup protected page admitted")
	}
	for _, sid := range []string{"S-TAB-1", "S-TAB-2", "S-PAGE-1", "S-PAGE-2"} {
		if g.sessions[sid] == nil {
			t.Fatalf("startup protected page detached ordinary %s", sid)
		}
		if emptyURLHoldDetachIndex(sends, sid) >= 0 {
			t.Fatalf("startup protected page detached ordinary %s: %v", sid, sends)
		}
	}
}

func TestEmptyURLHoldIframeUnderHeldPageDoesNotDeliverParent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-H", "PAGE-H", "OPENER", true))
	if g.held["S-PAGE-H"] == nil {
		t.Fatal("expected empty page held")
	}
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-PAGE-H",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-IFRAME",
			"waitingForDebugger": false,
			"targetInfo": map[string]any{
				"targetId":      "IFRAME",
				"type":          "iframe",
				"url":           "https://ordinary.test/frame",
				"openerId":      "",
				"parentFrameId": "PAGE-H",
			},
		},
	}))
	ps := emptyURLHoldPayloads(v)
	if g.held["S-PAGE-H"] == nil || g.sessions["S-PAGE-H"] != nil {
		t.Fatalf("iframe under held empty page delivered the parent before classification: held=%v session=%v childSession=%v payloads=%v", g.held["S-PAGE-H"] != nil, g.sessions["S-PAGE-H"] != nil, g.sessions["S-IFRAME"] != nil, ps)
	}
	if emptyURLHoldAttachIndex(ps, "S-PAGE-H") >= 0 {
		t.Fatalf("held empty page announced via iframe child: %v", ps)
	}
}

func TestEmptyURLHoldWorkerUnderHeldPageDoesNotDeliverParent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-H", "PAGE-H", "OPENER", true))
	if g.held["S-PAGE-H"] == nil {
		t.Fatal("expected empty page held")
	}
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-PAGE-H",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-WORKER",
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": "WORKER", "type": "worker", "url": "https://ordinary.test/w.js", "openerId": ""},
		},
	}))
	ps := emptyURLHoldPayloads(v)
	if g.held["S-PAGE-H"] == nil || g.sessions["S-PAGE-H"] != nil {
		t.Fatalf("worker under held empty page delivered the parent before classification: held=%v session=%v childSession=%v payloads=%v", g.held["S-PAGE-H"] != nil, g.sessions["S-PAGE-H"] != nil, g.sessions["S-WORKER"] != nil, ps)
	}
	if emptyURLHoldAttachIndex(ps, "S-PAGE-H") >= 0 {
		t.Fatalf("held empty page announced via worker child: %v", ps)
	}
}

func TestEmptyURLHoldPageChildUnderHeldPageDoesNotDeliverParent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-H", "PAGE-H", "OPENER", true))
	if g.held["S-PAGE-H"] == nil {
		t.Fatal("expected empty page held")
	}
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-PAGE-H",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-CHILD",
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": "CHILD", "type": "page", "url": "https://ordinary.test/child", "openerId": ""},
		},
	}))
	ps := emptyURLHoldPayloads(v)
	if g.held["S-PAGE-H"] == nil || g.sessions["S-PAGE-H"] != nil {
		t.Fatalf("page child under held empty page delivered the parent before classification: held=%v session=%v childSession=%v payloads=%v", g.held["S-PAGE-H"] != nil, g.sessions["S-PAGE-H"] != nil, g.sessions["S-CHILD"] != nil, ps)
	}
	if emptyURLHoldAttachIndex(ps, "S-PAGE-H") >= 0 {
		t.Fatalf("held empty page announced via page child: %v", ps)
	}
}

func TestEmptyURLHoldAdoptKeepsFlatPageDetachEnvelope(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-A", "PAGE-A", "SHARED", true))
	if g.held["S-PAGE-A"] == nil {
		t.Fatal("expected flat empty page held")
	}
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-A", "TAB-A", "SHARED", true))
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://seven.sliccy.ai/a", "SHARED"))
	sends := emptyURLHoldSends(v)
	if emptyURLHoldDetachEnvelope(sends, "S-PAGE-A", "S-TAB-A") >= 0 {
		t.Fatalf("page detach envelope must stay browser-level even if adopt rewrote bookkeeping parent: %v", sends)
	}
	if g.hidden["S-PAGE-A"] || emptyURLHoldDetachEnvelope(sends, "S-PAGE-A", "") >= 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-A") >= 0 {
		return
	}
	if g.sessions["S-PAGE-A"] != nil {
		t.Fatalf("protected flat page left a client session without a browser-level detach: %v", sends)
	}
}

func emptyURLHoldClientDetachSession(s string) (string, bool) {
	var m struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
		} `json:"params"`
	}
	if json.Unmarshal([]byte(s), &m) != nil || m.Method != "Target.detachedFromTarget" {
		return "", false
	}
	return m.Params.SessionID, m.Params.SessionID != ""
}

func emptyURLHoldClientDetachIndex(ss []string, sessionID string) int {
	for i, s := range ss {
		if id, ok := emptyURLHoldClientDetachSession(s); ok && id == sessionID {
			return i
		}
	}
	return -1
}

func emptyURLHoldClientDetachCount(ss []string, sessionID string) int {
	n := 0
	for _, s := range ss {
		if id, ok := emptyURLHoldClientDetachSession(s); ok && id == sessionID {
			n++
		}
	}
	return n
}

func emptyURLAssertClientSawTabDetach(t *testing.T, payloads []string, tabSession string) {
	t.Helper()
	if n := emptyURLHoldClientDetachCount(payloads, tabSession); n != 1 {
		t.Fatalf("client must see exactly one Target.detachedFromTarget for announced %s, got %d in %v", tabSession, n, payloads)
	}
}

func TestEmptyURLHoldNestedProtectedClientSeesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	tabClient := emptyURLHoldPayloads(g.fromBrowser(emptyURLTabAttach(true)))
	if emptyURLHoldAttachIndex(tabClient, "S-TAB") < 0 {
		t.Fatal("expected announced tab before nested protect")
	}
	_ = g.fromBrowser(emptyURLPageAttach(true))
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://seven.sliccy.ai/popup"))
	emptyURLAssertClientSawTabDetach(t, emptyURLHoldPayloads(v), "S-TAB")
}

func TestEmptyURLHoldFlatProtectedClientSeesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, 1)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	emptyURLAssertClientSawTabDetach(t, emptyURLHoldPayloads(v), "S-TAB-1")
}

func TestEmptyURLHoldFlatExpiredClientSeesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, 1)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-E", "PAGE-E", "OPENER", true))
	page, _ := g.expireHold("S-PAGE-E")
	emptyURLAssertClientSawTabDetach(t, emptyURLHoldPayloads(browserVerdict{page: page}), "S-TAB-1")
}

func emptyURLAssertFlatProtectedClientDetachesAll(t *testing.T, n int) {
	t.Helper()
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, n)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	ps := emptyURLHoldPayloads(v)
	for i := 1; i <= n; i++ {
		sid := fmt.Sprintf("S-TAB-%d", i)
		emptyURLAssertClientSawTabDetach(t, ps, sid)
	}
}

func TestEmptyURLHoldFlatProtectedClientDetachesOneUnmatchedTab(t *testing.T) {
	emptyURLAssertFlatProtectedClientDetachesAll(t, 1)
}
func TestEmptyURLHoldFlatProtectedClientDetachesTwoUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatProtectedClientDetachesAll(t, 2)
}
func TestEmptyURLHoldFlatProtectedClientDetachesThreeUnmatchedTabs(t *testing.T) {
	emptyURLAssertFlatProtectedClientDetachesAll(t, 3)
}

func TestEmptyURLHoldFlatMatchDoesNotShieldProtectedPopupTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-B", "TAB-B", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-B", "PAGE-B", "OPENER", true))
	if g.sessions["S-TAB-B"] == nil || g.held["S-PAGE-B"] == nil {
		t.Fatal("expected TabB announced and page B held")
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-A", "PAGE-A", "OPENER", "https://ordinary.test/a", false))
	if g.sessions["S-PAGE-A"] == nil {
		t.Fatalf("ordinary flat page A should classify: %v", emptyURLHoldPayloads(v))
	}
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://seven.sliccy.ai/b", "OPENER"))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if g.sessions["S-TAB-B"] != nil || (emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 && !g.hidden["S-TAB-B"] && emptyURLHoldClientDetachIndex(ps, "S-TAB-B") < 0) {
		t.Fatalf("flat match must not shield TabB from page B protect: sessions=%v payloads=%v sends=%v", g.sessions["S-TAB-B"] != nil, ps, sends)
	}
}

func TestEmptyURLHoldStaleEmptyTabOutsideHoldWindowUntouched(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-OLD", "TAB-OLD", "", true))
	if s := g.sessions["S-TAB-OLD"]; s != nil {
		s.at = time.Now().Add(-2 * HoldTimeout)
	}
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-E", "PAGE-E", "OPENER", true))
	if h := g.held["S-PAGE-E"]; h != nil {
		h.at = time.Now()
	}
	_, _ = g.expireHold("S-PAGE-E")
	if g.sessions["S-TAB-OLD"] == nil {
		t.Fatal("stale empty tab outside the hold window must not be detached by an unrelated expiry")
	}
}

func TestEmptyURLHoldStockOrdinaryFlatDoesNotDetachOwnTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	client := emptyURLAnnounceFlatTabs(t, g, 1)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-1", "PAGE-1", "OPENER", "https://ordinary.test/popup", false))
	client = append(client, emptyURLHoldPayloads(v)...)
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-1", "S-PAGE-1")
	if emptyURLHoldClientDetachIndex(client, "S-TAB-1") >= 0 {
		t.Fatalf("stock ordinary flat release must not detach its tab: %v", client)
	}
}

func TestEmptyURLHoldFailedLoadProtectedDetachesAnnouncedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-FL", "TAB-FL", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-FL", "S-PAGE-FL", "PAGE-FL", "OPENER", true))
	if g.sessions["S-TAB-FL"] == nil || g.held["S-PAGE-FL"] == nil {
		t.Fatal("expected announced tab and held page before failed-load protect")
	}
	g.targets["PAGE-FL"].url = "chrome-error://chromewebdata/"
	g.setErrorProofLocked("PAGE-FL", errorProofProtected)
	send := g.finishFailedLoadHeldLocked("PAGE-FL", true)
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	ps := emptyURLHoldPayloads(browserVerdict{send: send, page: page})
	if g.sessions["S-TAB-FL"] != nil {
		t.Fatalf("failed-load protected hide left announced tab session: payloads=%v sends=%v", ps, send)
	}
	emptyURLAssertClientSawTabDetach(t, ps, "S-TAB-FL")
}

func TestEmptyURLHoldDestroyWhileHeldDetachesAnnouncedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-D", "TAB-D", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-D", "S-PAGE-D", "PAGE-D", "OPENER", true))
	if g.held["S-PAGE-D"] == nil || g.sessions["S-TAB-D"] == nil {
		t.Fatal("expected held page under announced tab")
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-D"},
	}))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if g.sessions["S-TAB-D"] != nil || (emptyURLHoldDetachIndex(sends, "S-TAB-D") < 0 && emptyURLHoldClientDetachIndex(ps, "S-TAB-D") < 0 && !g.hidden["S-TAB-D"]) {
		t.Fatalf("destroy while held left announced tab: sessions=%v payloads=%v sends=%v", g.sessions["S-TAB-D"] != nil, ps, sends)
	}
}

func TestEmptyURLHoldTabWithOpenerIsFlatHideCandidate(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-OP", "TAB-OP", "OPENER", true))
	if g.sessions["S-TAB-OP"] == nil {
		t.Fatal("expected empty tab with opener announced")
	}
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-P", "PAGE-P", "OPENER", "https://seven.sliccy.ai/p", false))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if g.sessions["S-TAB-OP"] != nil || (emptyURLHoldDetachIndex(sends, "S-TAB-OP") < 0 && emptyURLHoldClientDetachIndex(ps, "S-TAB-OP") < 0 && !g.hidden["S-TAB-OP"]) {
		t.Fatalf("empty-at-announce tab with opener must still be a flat-hide candidate: sessions=%v payloads=%v sends=%v", g.sessions["S-TAB-OP"] != nil, ps, sends)
	}
}

func TestEmptyURLHoldDetachThenDestroyDetachesAnnouncedTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-DTD", "TAB-DTD", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-DTD", "S-PAGE-DTD", "PAGE-DTD", "OPENER", true))
	if g.sessions["S-TAB-DTD"] == nil || g.held["S-PAGE-DTD"] == nil {
		t.Fatal("expected announced tab and held page before Chrome page detach")
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-PAGE-DTD", "targetId": "PAGE-DTD"},
	}))
	ps := emptyURLHoldPayloads(v)
	v2 := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-DTD"},
	}))
	ps = append(ps, emptyURLHoldPayloads(v2)...)
	if g.sessions["S-TAB-DTD"] != nil {
		t.Fatalf("detach-then-destroy left announced tab attached: payloads=%v", ps)
	}
	emptyURLAssertClientSawTabDetach(t, ps, "S-TAB-DTD")
}

func TestEmptyURLHoldDestroyThenDetachDetachesAnnouncedTabOnce(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-DTD2", "TAB-DTD2", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-DTD2", "S-PAGE-DTD2", "PAGE-DTD2", "OPENER", true))
	if g.sessions["S-TAB-DTD2"] == nil || g.held["S-PAGE-DTD2"] == nil {
		t.Fatal("expected announced tab and held page before destroy-then-detach")
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-DTD2"},
	}))
	ps := emptyURLHoldPayloads(v)
	v2 := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-PAGE-DTD2", "targetId": "PAGE-DTD2"},
	}))
	ps = append(ps, emptyURLHoldPayloads(v2)...)
	if g.sessions["S-TAB-DTD2"] != nil {
		t.Fatalf("destroy-then-detach left announced tab attached: payloads=%v", ps)
	}
	emptyURLAssertClientSawTabDetach(t, ps, "S-TAB-DTD2")
}

func TestEmptyURLHoldPageDetachAfterTabGoneDoesNotDouble(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-ADT", "TAB-ADT", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-ADT", "S-PAGE-ADT", "PAGE-ADT", "OPENER", true))
	if g.sessions["S-TAB-ADT"] == nil || g.held["S-PAGE-ADT"] == nil {
		t.Fatal("expected announced tab and held page")
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-ADT", "targetId": "TAB-ADT"},
	}))
	ps := emptyURLHoldPayloads(v)
	if g.sessions["S-TAB-ADT"] != nil {
		t.Fatal("expected tab session cleared by Chrome tab detach")
	}
	v2 := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-PAGE-ADT", "targetId": "PAGE-ADT"},
	}))
	ps = append(ps, emptyURLHoldPayloads(v2)...)
	if n := emptyURLHoldClientDetachCount(ps, "S-TAB-ADT"); n != 1 {
		t.Fatalf("page detach after tab already gone must not synthesize a second tab detach, got %d in %v", n, ps)
	}
}

func TestEmptyURLHoldPageBeforeTabProtectedHide_KnownResidual(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-PBT", "PAGE-PBT", "", "https://seven.sliccy.ai/noopener", false))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-PBT") >= 0 || g.sessions["S-PAGE-PBT"] != nil || g.held["S-PAGE-PBT"] != nil {
		t.Fatalf("flat protected page-before-tab must hide the page without announcing it: payloads=%v", ps)
	}
	tabV := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-PBT", "TAB-PBT", "", true))
	tabClient := emptyURLHoldPayloads(tabV)
	if emptyURLHoldAttachIndex(tabClient, "S-TAB-PBT") < 0 || g.sessions["S-TAB-PBT"] == nil {
		t.Fatal("expected empty tab announced after the protected page hide")
	}
	if emptyURLHoldClientDetachIndex(tabClient, "S-TAB-PBT") >= 0 {
		t.Fatalf("KnownResidual pin broken: a post-hide empty-tab detach appeared (must not use a window rule that can hit createTarget tabs): %v", tabClient)
	}
}

func emptyURLHoldArmBlockChildPage(t *testing.T, g *guard, tabSession, tabID, pageSession, pageID string) {
	t.Helper()
	_ = g.fromBrowser(emptyURLNamedTabAttach(tabSession, tabID, "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach(tabSession, pageSession, pageID, "OPENER", true))
	if g.sessions[tabSession] == nil || g.held[pageSession] == nil {
		t.Fatal("expected announced tab and held page before Fetch block-child")
	}
}

func emptyURLHoldPausedDocument(session, reqID, networkID, frameID string) []byte {
	params := map[string]any{
		"requestId":    reqID,
		"networkId":    networkID,
		"frameId":      frameID,
		"resourceType": "Document",
		"request":      map[string]any{"url": "https://seven.sliccy.ai/child"},
	}
	return encode(map[string]any{
		"sessionId": session,
		"method":    "Fetch.requestPaused",
		"params":    params,
	})
}

func emptyURLHoldAssertPausedFlushesTabDetach(t *testing.T, g *guard, v browserVerdict, tabSession, label string) {
	t.Helper()
	ps := emptyURLHoldPayloads(v)
	if len(g.pendingPage) > 0 {
		t.Fatalf("%s left client tab detach parked on pendingPage: pending=%v payloads=%v", label, len(g.pendingPage), ps)
	}
	if g.sessions[tabSession] != nil {
		t.Fatalf("%s blocked held child left announced tab attached: payloads=%v", label, ps)
	}
	emptyURLAssertClientSawTabDetach(t, ps, tabSession)
}

func TestEmptyURLHoldNavFetchBlockChildFlushesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldArmBlockChildPage(t, g, "S-TAB-BC", "TAB-BC", "S-PAGE-BC", "PAGE-BC")
	nt := &navTarget{target: "OPENER", session: "NAV-BC", ready: make(chan struct{})}
	g.navTargets["OPENER"] = nt
	g.navSessions["NAV-BC"] = nt
	v := g.fromBrowser(emptyURLHoldPausedDocument("NAV-BC", "REQ-BC", "NET-BC", "PAGE-BC"))
	emptyURLHoldAssertPausedFlushesTabDetach(t, g, v, "S-TAB-BC", "nav Fetch.requestPaused")
}

func TestEmptyURLHoldPausedNoNavSessionFlushesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldArmBlockChildPage(t, g, "S-TAB-PN", "TAB-PN", "S-PAGE-PN", "PAGE-PN")
	v := g.fromBrowser(emptyURLHoldPausedDocument("S-OPENER", "REQ-PN", "NET-PN", "PAGE-PN"))
	emptyURLHoldAssertPausedFlushesTabDetach(t, g, v, "S-TAB-PN", "Fetch.requestPaused with no nav session")
}

func TestEmptyURLHoldPausedNavSeenFlushesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldArmBlockChildPage(t, g, "S-TAB-PS", "TAB-PS", "S-PAGE-PS", "PAGE-PS")
	nt := &navTarget{target: "OPENER", session: "NAV-PS", ready: make(chan struct{})}
	g.navTargets["OPENER"] = nt
	g.navSessions["NAV-PS"] = nt
	g.navSeen[requestKey{"OPENER", "NET-PS"}] = true
	v := g.fromBrowser(emptyURLHoldPausedDocument("S-OPENER", "REQ-PS", "NET-PS", "PAGE-PS"))
	emptyURLHoldAssertPausedFlushesTabDetach(t, g, v, "S-TAB-PS", "Fetch.requestPaused with navSeen")
}

func TestEmptyURLHoldPausedEmptyNetworkIDFlushesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldArmBlockChildPage(t, g, "S-TAB-PE", "TAB-PE", "S-PAGE-PE", "PAGE-PE")
	nt := &navTarget{target: "OPENER", session: "NAV-PE", ready: make(chan struct{})}
	g.navTargets["OPENER"] = nt
	g.navSessions["NAV-PE"] = nt
	v := g.fromBrowser(emptyURLHoldPausedDocument("S-OPENER", "REQ-PE", "", "PAGE-PE"))
	emptyURLHoldAssertPausedFlushesTabDetach(t, g, v, "S-TAB-PE", "Fetch.requestPaused with empty networkId")
}

func TestEmptyURLHoldProtectedTabCascadesShownWorkerChild(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-W", "TAB-W", "", true))
	wv := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-TAB-W",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-WORKER-W",
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": "WORKER-W", "type": "worker", "url": "https://ordinary.test/w.js", "openerId": ""},
		},
	}))
	if g.sessions["S-WORKER-W"] == nil {
		t.Fatalf("expected worker shown under announced tab: payloads=%v held=%v", emptyURLHoldPayloads(wv), g.held["S-WORKER-W"] != nil)
	}
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-W", "S-PAGE-W", "PAGE-W", "OPENER", true))
	if g.held["S-PAGE-W"] == nil {
		t.Fatal("expected page held under announced tab")
	}
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-W", "page", "https://seven.sliccy.ai/w", "OPENER"))
	ps := emptyURLHoldPayloads(v)
	emptyURLAssertClientSawTabDetach(t, ps, "S-TAB-W")
	if emptyURLHoldClientDetachIndex(ps, "S-WORKER-W") < 0 {
		echo := g.fromBrowser(encode(map[string]any{
			"method": "Target.detachedFromTarget",
			"params": map[string]any{"sessionId": "S-TAB-W", "targetId": "TAB-W"},
		}))
		ps = append(ps, emptyURLHoldPayloads(echo)...)
	}
	if emptyURLHoldClientDetachIndex(ps, "S-WORKER-W") < 0 || g.sessions["S-WORKER-W"] != nil {
		t.Fatalf("protected tab detach must cascade shown worker child as protectLocked does: payloads=%v session=%v", ps, g.sessions["S-WORKER-W"] != nil)
	}
}

func emptyURLHoldAssertHeldPageNotGhost(t *testing.T, g *guard, pageSession, pageTarget string, hideSends []string) {
	t.Helper()
	if g.held[pageSession] != nil {
		t.Fatalf("held page %s must not remain in g.held after parent tab detach", pageSession)
	}
	ev := g.fromBrowser(encode(map[string]any{
		"sessionId": pageSession,
		"method":    "Runtime.consoleAPICalled",
		"params":    map[string]any{"type": "log", "args": []any{}},
	}))
	if ev.forward != nil || emptyURLHoldContains(emptyURLHoldPayloads(ev), "consoleAPICalled") {
		t.Fatalf("events on orphaned held page session must be dropped (no forwarding ghost), got forward=%s payloads=%v held=%v hidden=%v refused=%v gone=%v", ev.forward, emptyURLHoldPayloads(ev), g.held[pageSession] != nil, g.hidden[pageSession], g.refused[pageSession], g.gone[pageSession])
	}
	if g.held[pageSession] != nil {
		t.Fatalf("held page %s must leave g.held after parent tab detach", pageSession)
	}
	if emptyURLHoldDetachIndex(hideSends, pageSession) < 0 {
		t.Fatalf("guard must send Target.detachFromTarget for held page %s: %v", pageSession, hideSends)
	}
	changed := g.fromBrowser(emptyURLNamedRelayChanged(pageTarget, "page", "https://seven.sliccy.ai/ghost", "OPENER"))
	ps := emptyURLHoldPayloads(changed)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions[pageSession] != nil {
		t.Fatalf("later protected targetInfoChanged must not admit ghost page session: payloads=%v session=%v", ps, g.sessions[pageSession] != nil)
	}
	late := g.fromBrowser(encode(map[string]any{
		"sessionId": pageSession,
		"method":    "Network.requestWillBeSent",
		"params":    map[string]any{"requestId": "GHOST", "request": map[string]any{"url": "https://seven.sliccy.ai/n"}},
	}))
	if late.forward != nil {
		t.Fatalf("post-classify events on ghost page session must be dropped, got %s", late.forward)
	}
}

func TestEmptyURLHoldClientTabDetachHidesNestedHeldPage(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-GH", "TAB-GH", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-GH", "S-PAGE-GH", "PAGE-GH", "OPENER", true))
	if g.sessions["S-TAB-GH"] == nil || g.held["S-PAGE-GH"] == nil {
		t.Fatal("expected announced tab and nested held page")
	}
	pv := g.fromCommand(&command{id: 90, method: "Target.detachFromTarget", params: map[string]any{"sessionId": "S-TAB-GH"}}, 90, true)
	sends := make([]string, 0, len(pv.send))
	for _, b := range pv.send {
		sends = append(sends, string(b))
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-GH", "targetId": "TAB-GH"},
	}))
	sends = append(sends, emptyURLHoldSends(v)...)
	emptyURLHoldAssertHeldPageNotGhost(t, g, "S-PAGE-GH", "PAGE-GH", sends)
}

func TestEmptyURLHoldProtectTabCascadesHideNestedHeldPage(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-PT", "TAB-PT", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-PT", "S-PAGE-PT", "PAGE-PT", "OPENER", true))
	if g.sessions["S-TAB-PT"] == nil || g.held["S-PAGE-PT"] == nil {
		t.Fatal("expected announced tab and nested held page before protectLocked on tab")
	}
	g.mu.Lock()
	tab := g.targets["TAB-PT"]
	tab.protected = true
	protSend := g.protectLocked(tab)
	g.mu.Unlock()
	sends := emptyURLHoldSends(browserVerdict{send: protSend})
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-PT", "targetId": "TAB-PT"},
	}))
	sends = append(sends, emptyURLHoldSends(v)...)
	emptyURLHoldAssertHeldPageNotGhost(t, g, "S-PAGE-PT", "PAGE-PT", sends)
}

func TestEmptyURLHoldShownPageCascadeUnchanged(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-SH", "TAB-SH", "", true))
	pv := g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-SH", "S-PAGE-SH", "PAGE-SH", "OPENER", "https://ordinary.test/shown", false))
	if g.sessions["S-PAGE-SH"] == nil {
		t.Fatalf("expected ordinary nested page shown under tab: payloads=%v held=%v", emptyURLHoldPayloads(pv), g.held["S-PAGE-SH"] != nil)
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-SH", "targetId": "TAB-SH"},
	}))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldClientDetachIndex(ps, "S-PAGE-SH") < 0 || g.sessions["S-PAGE-SH"] != nil {
		t.Fatalf("ordinary shown page under detached tab must still cascade client detach: payloads=%v session=%v", ps, g.sessions["S-PAGE-SH"] != nil)
	}
}

func TestEmptyURLHoldOrphanedHeldPageCommandIsMissingSession(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-MS", "TAB-MS", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-MS", "S-PAGE-MS", "PAGE-MS", "OPENER", true))
	if g.held["S-PAGE-MS"] == nil {
		t.Fatal("expected nested page held")
	}
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-MS", "targetId": "TAB-MS"},
	}))
	if g.held["S-PAGE-MS"] != nil {
		t.Fatal("expected held page cleaned on parent tab detach")
	}
	ev := g.fromBrowser(encode(map[string]any{
		"sessionId": "S-PAGE-MS",
		"method":    "Runtime.consoleAPICalled",
		"params":    map[string]any{"type": "log"},
	}))
	if ev.forward != nil {
		t.Fatalf("orphaned held page events must stay dropped: %s", ev.forward)
	}
	pv := g.fromCommand(&command{id: 96, method: "Page.enable", session: "S-PAGE-MS", params: map[string]any{}}, 96, true)
	if len(pv.forward) > 0 {
		t.Fatalf("client command on never-seen held page was forwarded: %s", pv.forward)
	}
	if len(pv.reply) == 0 {
		t.Fatal("expected missing-session reply for never-seen held page")
	}
	var m message
	if json.Unmarshal(pv.reply, &m) != nil || m.Error == nil {
		t.Fatalf("expected error reply: %s", pv.reply)
	}
	var err struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(m.Error, &err) != nil || err.Code != -32001 || err.Message != "Session with given id not found." {
		t.Fatalf("never-seen held page must answer missing-session -32001, got %s", pv.reply)
	}
}

func TestEmptyURLHoldClientTabDetachStillHidesHeldWorker(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-HW", "TAB-HW", "", true))
	_ = g.fromBrowser(encode(map[string]any{
		"sessionId": "S-TAB-HW",
		"method":    "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-WORKER-HW",
			"waitingForDebugger": true,
			"targetInfo":         map[string]any{"targetId": "WORKER-HW", "type": "worker", "url": "", "openerId": ""},
		},
	}))
	if g.held["S-WORKER-HW"] == nil {
		t.Fatal("expected unresolved worker held under tab")
	}
	pv := g.fromCommand(&command{id: 91, method: "Target.detachFromTarget", params: map[string]any{"sessionId": "S-TAB-HW"}}, 91, true)
	sends := make([]string, 0, len(pv.send))
	for _, b := range pv.send {
		sends = append(sends, string(b))
	}
	if g.held["S-WORKER-HW"] != nil || emptyURLHoldDetachIndex(sends, "S-WORKER-HW") < 0 && !g.hidden["S-WORKER-HW"] {
		t.Fatalf("client tab detach must still hide held worker child: held=%v hidden=%v sends=%v", g.held["S-WORKER-HW"] != nil, g.hidden["S-WORKER-HW"], sends)
	}
}

func emptyURLHoldArmBrowserEnvelope(t *testing.T, g *guard) {
	t.Helper()
	g.targets["BROWSER"] = &target{id: "BROWSER", typ: "browser"}
	g.sessions["S-BR"] = &session{id: "S-BR", target: "BROWSER", typ: "browser"}
	g.browserSessions["S-BR"] = true
}

func emptyURLHoldBrowserEnvelopeHeld(t *testing.T, g *guard, tabSession, tabID, pageSession, pageID string) {
	t.Helper()
	emptyURLHoldArmBrowserEnvelope(t, g)
	_ = g.fromBrowser(emptyURLNamedTabAttach(tabSession, tabID, "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-BR", pageSession, pageID, "OPENER", true))
	if g.sessions[tabSession] == nil || g.held[pageSession] == nil || g.held[pageSession].parent != "S-BR" {
		t.Fatalf("expected announced tab and flat page held under browser session: tab=%v held=%v parent=%q", g.sessions[tabSession] != nil, g.held[pageSession] != nil, func() string {
			if g.held[pageSession] == nil {
				return ""
			}
			return g.held[pageSession].parent
		}())
	}
}

func emptyURLHoldAssertTabDetached(t *testing.T, g *guard, tabSession string, ps []string, sends []string, why string) {
	t.Helper()
	if g.sessions[tabSession] != nil || (emptyURLHoldDetachIndex(sends, tabSession) < 0 && emptyURLHoldClientDetachIndex(ps, tabSession) < 0 && !g.hidden[tabSession]) {
		t.Fatalf("%s left announced tab attached under browser-session envelope: sessions=%v payloads=%v sends=%v", why, g.sessions[tabSession] != nil, ps, sends)
	}
}

func TestEmptyURLHoldBrowserEnvelopeProtectedDetachesTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldBrowserEnvelopeHeld(t, g, "S-TAB-BR", "TAB-BR", "S-PAGE-BR", "PAGE-BR")
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-BR", "page", "https://seven.sliccy.ai/br", "OPENER"))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-BR", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "browser-envelope protected classify")
}

func TestEmptyURLHoldBrowserEnvelopeExpiryDetachesTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldBrowserEnvelopeHeld(t, g, "S-TAB-BRE", "TAB-BRE", "S-PAGE-BRE", "PAGE-BRE")
	page, send := g.expireHold("S-PAGE-BRE")
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-BRE", emptyURLHoldPayloads(browserVerdict{page: page}), emptyURLHoldSends(browserVerdict{send: send}), "browser-envelope expiry")
}

func TestEmptyURLHoldBrowserEnvelopeFailedLoadProtectedDetachesTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldBrowserEnvelopeHeld(t, g, "S-TAB-BRFL", "TAB-BRFL", "S-PAGE-BRFL", "PAGE-BRFL")
	g.targets["PAGE-BRFL"].url = "chrome-error://chromewebdata/"
	g.setErrorProofLocked("PAGE-BRFL", errorProofProtected)
	send := g.finishFailedLoadHeldLocked("PAGE-BRFL", true)
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	ps := emptyURLHoldPayloads(browserVerdict{send: send, page: page})
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-BRFL", ps, emptyURLHoldSends(browserVerdict{send: send}), "browser-envelope failed-load protected")
}

func TestEmptyURLHoldBrowserEnvelopeDestroyWhileHeldDetachesTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldBrowserEnvelopeHeld(t, g, "S-TAB-BRD", "TAB-BRD", "S-PAGE-BRD", "PAGE-BRD")
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-BRD"},
	}))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-BRD", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "browser-envelope destroy-while-held")
}

func TestEmptyURLHoldBrowserEnvelopeImmediateHideDetachesTab(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldArmBrowserEnvelope(t, g)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-BRI", "TAB-BRI", "", true))
	v := g.fromBrowser(emptyURLNamedPageAttachURL("S-BR", "S-PAGE-BRI", "PAGE-BRI", "OPENER", "https://seven.sliccy.ai/imm", false))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-BRI", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "browser-envelope immediate protect hide")
}

func TestEmptyURLHoldBrowserEnvelopeOrdinaryDeliversUnchanged(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldBrowserEnvelopeHeld(t, g, "S-TAB-BRO", "TAB-BRO", "S-PAGE-BRO", "PAGE-BRO")
	tabClient := []string{}
	if s := g.sessions["S-TAB-BRO"]; s != nil {
		tabClient = append(tabClient, string(encode(map[string]any{
			"method": "Target.attachedToTarget",
			"params": map[string]any{"sessionId": "S-TAB-BRO", "waitingForDebugger": true, "targetInfo": map[string]any{"targetId": "TAB-BRO", "type": "tab", "url": ""}},
		})))
	}
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-BRO", "page", "https://ordinary.test/br", "OPENER"))
	ps := append(tabClient, emptyURLHoldPayloads(v)...)
	if g.sessions["S-TAB-BRO"] == nil || g.sessions["S-PAGE-BRO"] == nil || emptyURLHoldAttachIndex(emptyURLHoldPayloads(v), "S-PAGE-BRO") < 0 {
		t.Fatalf("browser-envelope ordinary deliver must keep tab and announce page: sessions tab=%v page=%v payloads=%v", g.sessions["S-TAB-BRO"] != nil, g.sessions["S-PAGE-BRO"] != nil, ps)
	}
	if emptyURLHoldClientDetachIndex(emptyURLHoldPayloads(v), "S-TAB-BRO") >= 0 {
		t.Fatalf("browser-envelope ordinary must not detach its tab: %v", emptyURLHoldPayloads(v))
	}
}

func emptyURLHoldAssertMissingSessionReply(t *testing.T, reply []byte, label string) {
	t.Helper()
	if len(reply) == 0 {
		t.Fatalf("%s: expected missing-session reply", label)
	}
	var m message
	if json.Unmarshal(reply, &m) != nil || m.Error == nil {
		t.Fatalf("%s: expected error reply: %s", label, reply)
	}
	var err struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(m.Error, &err) != nil || err.Code != -32001 || err.Message != "Session with given id not found." {
		t.Fatalf("%s: want missing-session -32001, got %s", label, reply)
	}
}

func TestEmptyURLHoldExpiryTabCommandIsMissingSessionNotProtected(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-EX", "TAB-EX", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-EX", "S-PAGE-EX", "PAGE-EX", "OPENER", true))
	page, send := g.expireHold("S-PAGE-EX")
	ps := emptyURLHoldPayloads(browserVerdict{page: page})
	sends := emptyURLHoldSends(browserVerdict{send: send})
	if g.sessions["S-TAB-EX"] != nil && emptyURLHoldClientDetachIndex(ps, "S-TAB-EX") < 0 && emptyURLHoldDetachIndex(sends, "S-TAB-EX") < 0 {
		t.Fatalf("expiry must detach announced tab: payloads=%v sends=%v", ps, sends)
	}
	for _, line := range logs {
		if strings.Contains(line, "protected origin") {
			t.Fatalf("unclassified expiry used protected-origin log: %s", line)
		}
	}
	pv := g.fromCommand(&command{id: 96, method: "Page.enable", session: "S-TAB-EX", params: map[string]any{}}, 96, true)
	if len(pv.forward) > 0 {
		t.Fatalf("expiry tab command was forwarded: %s", pv.forward)
	}
	if strings.Contains(string(pv.reply), "seven's origin") || strings.Contains(string(pv.reply), `"code":-32000`) {
		t.Fatalf("expiry must not answer protected-origin refuse on the detached tab: %s", pv.reply)
	}
	emptyURLHoldAssertMissingSessionReply(t, pv.reply, "expiry tab command")
}

func TestEmptyURLHoldProtectedTabCommandStaysRefusedTarget(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-PR", "TAB-PR", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-PR", "S-PAGE-PR", "PAGE-PR", "OPENER", true))
	_ = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-PR", "page", "https://seven.sliccy.ai/pr", "OPENER"))
	pv := g.fromCommand(&command{id: 97, method: "Page.enable", session: "S-TAB-PR", params: map[string]any{}}, 97, true)
	if len(pv.forward) > 0 {
		t.Fatalf("protected tab command was forwarded: %s", pv.forward)
	}
	if !strings.Contains(string(pv.reply), "seven's origin") {
		t.Fatalf("protected path today answers refusedTarget on the detached tab session, got %s", pv.reply)
	}
}

func emptyURLHoldPageThenLaterTab(t *testing.T, g *guard, browserRoot bool, pageSession, pageID, tabSession, tabID string) {
	t.Helper()
	if browserRoot {
		emptyURLHoldArmBrowserEnvelope(t, g)
		_ = g.fromBrowser(emptyURLNamedPageAttach("S-BR", pageSession, pageID, "", true))
	} else {
		_ = g.fromBrowser(emptyURLNamedPageAttachFirst(pageSession, pageID, "", true))
	}
	if g.held[pageSession] == nil {
		t.Fatal("expected flat empty page held before later tab announce")
	}
	if h := g.held[pageSession]; h != nil {
		h.at = time.Now().Add(-time.Millisecond)
	}
	_ = g.fromBrowser(emptyURLNamedTabAttach(tabSession, tabID, "", true))
	if g.sessions[tabSession] == nil {
		t.Fatal("expected empty tab announced after the page was held")
	}
	if s, h := g.sessions[tabSession], g.held[pageSession]; s != nil && h != nil && !s.at.After(h.at) {
		t.Fatal("expected tab announce time after held page start")
	}
}

func TestEmptyURLHoldPageThenTabProtectedDetachesTabEmptyEnvelope(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, false, "S-PAGE-PT1", "PAGE-PT1", "S-TAB-PT1", "TAB-PT1")
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-PT1", "page", "https://seven.sliccy.ai/pt1", ""))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PT1", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "page-then-tab empty-envelope protected")
}

func TestEmptyURLHoldPageThenTabProtectedDetachesTabBrowserRoot(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, true, "S-PAGE-PT2", "PAGE-PT2", "S-TAB-PT2", "TAB-PT2")
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-PT2", "page", "https://seven.sliccy.ai/pt2", ""))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PT2", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "page-then-tab browser-root protected")
}

func TestEmptyURLHoldPageThenTabExpiryDetachesTabEmptyEnvelope(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, false, "S-PAGE-PE1", "PAGE-PE1", "S-TAB-PE1", "TAB-PE1")
	page, send := g.expireHold("S-PAGE-PE1")
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PE1", emptyURLHoldPayloads(browserVerdict{page: page}), emptyURLHoldSends(browserVerdict{send: send}), "page-then-tab empty-envelope expiry")
}

func TestEmptyURLHoldPageThenTabExpiryDetachesTabBrowserRoot(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, true, "S-PAGE-PE2", "PAGE-PE2", "S-TAB-PE2", "TAB-PE2")
	page, send := g.expireHold("S-PAGE-PE2")
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PE2", emptyURLHoldPayloads(browserVerdict{page: page}), emptyURLHoldSends(browserVerdict{send: send}), "page-then-tab browser-root expiry")
}

func TestEmptyURLHoldPageThenTabFailedLoadProtectedDetachesTabEmptyEnvelope(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, false, "S-PAGE-PF1", "PAGE-PF1", "S-TAB-PF1", "TAB-PF1")
	g.targets["PAGE-PF1"].url = "chrome-error://chromewebdata/"
	g.setErrorProofLocked("PAGE-PF1", errorProofProtected)
	send := g.finishFailedLoadHeldLocked("PAGE-PF1", true)
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PF1", emptyURLHoldPayloads(browserVerdict{send: send, page: page}), emptyURLHoldSends(browserVerdict{send: send}), "page-then-tab empty-envelope failed-load")
}

func TestEmptyURLHoldPageThenTabFailedLoadProtectedDetachesTabBrowserRoot(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, true, "S-PAGE-PF2", "PAGE-PF2", "S-TAB-PF2", "TAB-PF2")
	g.targets["PAGE-PF2"].url = "chrome-error://chromewebdata/"
	g.setErrorProofLocked("PAGE-PF2", errorProofProtected)
	send := g.finishFailedLoadHeldLocked("PAGE-PF2", true)
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PF2", emptyURLHoldPayloads(browserVerdict{send: send, page: page}), emptyURLHoldSends(browserVerdict{send: send}), "page-then-tab browser-root failed-load")
}

func TestEmptyURLHoldPageThenTabDestroyWhileHeldDetachesTabEmptyEnvelope(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, false, "S-PAGE-PD1", "PAGE-PD1", "S-TAB-PD1", "TAB-PD1")
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-PD1"},
	}))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PD1", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "page-then-tab empty-envelope destroy")
}

func TestEmptyURLHoldPageThenTabDestroyWhileHeldDetachesTabBrowserRoot(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLHoldPageThenLaterTab(t, g, true, "S-PAGE-PD2", "PAGE-PD2", "S-TAB-PD2", "TAB-PD2")
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.targetDestroyed",
		"params": map[string]any{"targetId": "PAGE-PD2"},
	}))
	emptyURLHoldAssertTabDetached(t, g, "S-TAB-PD2", emptyURLHoldPayloads(v), emptyURLHoldSends(v), "page-then-tab browser-root destroy")
}

func TestEmptyURLHoldPageThenTabURLTabNotCandidate(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-PU", "PAGE-PU", "", true))
	if h := g.held["S-PAGE-PU"]; h != nil {
		h.at = time.Now().Add(-time.Millisecond)
	}
	_ = g.fromBrowser(emptyURLNamedTabAttachURL("S-TAB-PU", "TAB-PU", "", "https://ordinary.test/known", false))
	page, send := g.expireHold("S-PAGE-PU")
	if g.sessions["S-TAB-PU"] == nil {
		t.Fatalf("URL tab must not be an unmatched empty-tab candidate: payloads=%v sends=%v", emptyURLHoldPayloads(browserVerdict{page: page}), emptyURLHoldSends(browserVerdict{send: send}))
	}
}

func TestEmptyURLHoldWindowMatchLogsUnmatchedDetachNotAdoptedOrNested(t *testing.T) {
	const needle = "detached an unmatched empty tab by hold-window match"
	var logs []string
	capture := func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	g := emptyURLHoldGuardLog(t, capture)
	_ = emptyURLAnnounceFlatTabs(t, g, 1)
	_ = g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-WL", "PAGE-WL", "OPENER", "https://seven.sliccy.ai/wl", false))
	sawWindow := false
	for _, line := range logs {
		if strings.Contains(line, needle) && strings.Contains(line, "session=S-TAB-1") && strings.Contains(line, "held=PAGE-WL") && strings.Contains(line, "why=immediate hide") && strings.Contains(line, "window=") {
			sawWindow = true
			break
		}
	}
	if !sawWindow {
		t.Fatalf("window-matched unmatched empty tab detach missing hold-window match log: %v", logs)
	}
	logs = nil
	g2 := emptyURLHoldGuardLog(t, capture)
	_ = g2.fromBrowser(emptyURLNamedTabAttach("S-TAB-WN", "TAB-WN", "OPENER", true))
	_ = g2.fromBrowser(emptyURLNamedPageAttach("S-TAB-WN", "S-PAGE-WN", "PAGE-WN", "OPENER", true))
	_ = g2.fromBrowser(emptyURLNamedRelayChanged("PAGE-WN", "page", "https://seven.sliccy.ai/wn", "OPENER"))
	for _, line := range logs {
		if strings.Contains(line, needle) {
			t.Fatalf("nested parent tab detach must not log hold-window match: %s", line)
		}
	}
	logs = nil
	g3 := emptyURLHoldGuardLog(t, capture)
	_ = g3.fromBrowser(emptyURLNamedPageAttachFirst("S-PAGE-WA", "PAGE-WA", "OPENER", true))
	_ = g3.fromBrowser(emptyURLNamedTabAttach("S-TAB-WA", "TAB-WA", "OPENER", true))
	if h := g3.held["S-PAGE-WA"]; h == nil || h.adoptedTab != "S-TAB-WA" {
		adopted := ""
		if h != nil {
			adopted = h.adoptedTab
		}
		t.Fatalf("expected adopted flat tab before protected hide: held=%v adopted=%q", g3.held["S-PAGE-WA"] != nil, adopted)
	}
	_ = g3.fromBrowser(emptyURLNamedRelayChanged("PAGE-WA", "page", "https://seven.sliccy.ai/wa", "OPENER"))
	for _, line := range logs {
		if strings.Contains(line, needle) {
			t.Fatalf("adopted tab detach must not log hold-window match: %s", line)
		}
	}
}

func TestEmptyURLHoldExplicitAttachEmptyURLUsesDocConfirmNotPageHold(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetCreated",
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": "PAGE-EX", "type": "page", "url": "", "openerId": "SHARED"},
		},
	}))
	pv := g.fromCommand(&command{id: 8501, method: "Target.attachToTarget", params: map[string]any{"targetId": "PAGE-EX", "flatten": true}}, 8501, true)
	if pv.reply != nil {
		t.Fatalf("explicit attach refused before confirm: %s", pv.reply)
	}
	v := g.fromBrowser(encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-PAGE-EX",
			"waitingForDebugger": false,
			"targetInfo":         map[string]any{"targetId": "PAGE-EX", "type": "page", "url": "", "openerId": "SHARED"},
		},
	}))
	if g.held["S-PAGE-EX"] != nil {
		t.Fatal("explicit empty-URL attach must use document confirmation, not the empty-URL page hold")
	}
	asked := false
	for _, raw := range v.send {
		var m struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Method == "Target.getTargetInfo" && m.SessionID == "RELAY" {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("expected document-confirm Target.getTargetInfo, payloads=%v sends=%v", emptyURLHoldPayloads(v), emptyURLHoldSends(v))
	}
	if g.docInflight["PAGE-EX"] == nil || g.docInflight["PAGE-EX"].done {
		t.Fatal("expected an in-flight document confirmation for the explicit empty-URL attach")
	}
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-EX", "TAB-EX", "", true))
	if g.sessions["S-TAB-EX"] == nil {
		t.Fatal("empty tab auto-attach must still be announced while an explicit empty page is under document confirm")
	}
	g2 := emptyURLHoldGuard(t)
	_ = g2.fromBrowser(emptyURLPageAttachFirst(true))
	if g2.held["S-PAGE"] == nil {
		t.Fatal("non-explicit empty-URL page must still enter the empty-URL page hold")
	}
	_ = g2.fromBrowser(emptyURLTabAttach(true))
	if g2.sessions["S-TAB"] == nil {
		t.Fatal("non-explicit empty tab must still be announced under the empty-URL hold path")
	}
}
