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

func emptyURLMatchedTab(g *guard, sid string) bool {
	return g.matchedEmptyOpenerTabs[sid]
}

func TestEmptyURLHoldMatchedTabClearedOnForget(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = emptyURLAnnounceFlatTabs(t, g, 1)
	_ = g.fromBrowser(emptyURLNamedPageAttachURL("", "S-PAGE-1", "PAGE-1", "OPENER", "https://ordinary.test/popup", false))
	matched := emptyURLMatchedTab(g, "S-TAB-1")
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.detachedFromTarget",
		"params": map[string]any{"sessionId": "S-TAB-1", "targetId": "TAB-1"},
	}))
	if matched && emptyURLMatchedTab(g, "S-TAB-1") {
		t.Fatal("matchedEmptyOpenerTabs entry survived session forget")
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

func TestEmptyURLHoldNavFetchBlockChildFlushesTabDetach(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLNamedTabAttach("S-TAB-BC", "TAB-BC", "", true))
	_ = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-BC", "S-PAGE-BC", "PAGE-BC", "OPENER", true))
	if g.sessions["S-TAB-BC"] == nil || g.held["S-PAGE-BC"] == nil {
		t.Fatal("expected announced tab and held page before nav Fetch block")
	}
	nt := &navTarget{target: "OPENER", session: "NAV-BC", ready: make(chan struct{})}
	g.navTargets["OPENER"] = nt
	g.navSessions["NAV-BC"] = nt
	v := g.fromBrowser(encode(map[string]any{
		"sessionId": "NAV-BC",
		"method":    "Fetch.requestPaused",
		"params": map[string]any{
			"requestId":    "REQ-BC",
			"networkId":    "NET-BC",
			"frameId":      "PAGE-BC",
			"resourceType": "Document",
			"request":      map[string]any{"url": "https://seven.sliccy.ai/child"},
		},
	}))
	ps := emptyURLHoldPayloads(v)
	if len(g.pendingPage) > 0 {
		t.Fatalf("nav Fetch.requestPaused left client tab detach parked on pendingPage: pending=%v payloads=%v", len(g.pendingPage), ps)
	}
	if g.sessions["S-TAB-BC"] != nil {
		t.Fatalf("blocked held child left announced tab attached: payloads=%v", ps)
	}
	emptyURLAssertClientSawTabDetach(t, ps, "S-TAB-BC")
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
