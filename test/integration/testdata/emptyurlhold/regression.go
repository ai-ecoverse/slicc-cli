package cdp

import (
	"fmt"
	"strings"
	"testing"

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
	sends := emptyURLHoldSends(browserVerdict{send: g.expireHold("S-PAGE")})
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
	_ = g.expireHold("S-PAGE")
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
	sends := emptyURLHoldSends(browserVerdict{send: g.expireHold("S-PAGE-A")})
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
	sends = emptyURLHoldSends(browserVerdict{send: g.expireHold("S-PAGE-B")})
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
	if g.held["S-PAGE-A"].parent != "S-TAB-A" || g.held["S-PAGE-B"].parent != "S-TAB-B" {
		t.Fatalf("page-before-tab must adopt each held page under its announced tab: A=%q B=%q", g.held["S-PAGE-A"].parent, g.held["S-PAGE-B"].parent)
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
	sends := emptyURLHoldSends(browserVerdict{send: g.expireHold("S-PAGE-E")})
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
