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

func TestEmptyURLHoldTabWaitingIsHeldWithPage(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLTabAttach(true))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("empty-URL tab attach announced before classification: %v", ps)
	}
	if g.sessions["S-TAB"] != nil {
		t.Fatal("empty-URL tab session admitted before classification")
	}
	if g.held["S-TAB"] == nil {
		t.Fatal("empty-URL tab attach was not held")
	}
	if !g.held["S-TAB"].resumed {
		t.Fatal("held empty-URL tab was not guard-resumed")
	}
	v = g.fromBrowser(emptyURLPageAttach(true))
	ps = emptyURLHoldPayloads(v)
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
	emptyURLAttachPair(t, g, true)
	if g.sessions["S-TAB"] != nil || g.sessions["S-PAGE"] != nil {
		t.Fatal("client session admitted before URL classification")
	}
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://ordinary.test/popup"))
	ps := emptyURLHoldPayloads(v)
	tabAt := emptyURLHoldAttachIndex(ps, "S-TAB")
	pageAt := emptyURLHoldAttachIndex(ps, "S-PAGE")
	if tabAt < 0 || pageAt < 0 {
		t.Fatalf("ordinary classification missing tab/page attach: %v", ps)
	}
	if tabAt > pageAt {
		t.Fatalf("ordinary classification announced page before tab: %v", ps)
	}
	if g.held["S-TAB"] != nil || g.held["S-PAGE"] != nil {
		t.Fatal("ordinary classification left a hold behind")
	}
	if g.sessions["S-TAB"] == nil || g.sessions["S-PAGE"] == nil {
		t.Fatal("ordinary classification did not admit both sessions")
	}
}

func TestEmptyURLHoldProtectedAnnouncesNeither(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachPair(t, g, true)
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://seven.sliccy.ai/popup"))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("protected classification announced an attach: %v", ps)
	}
	if g.sessions["S-TAB"] != nil || g.sessions["S-PAGE"] != nil {
		t.Fatal("protected classification admitted a client session")
	}
	if g.held["S-TAB"] != nil || g.held["S-PAGE"] != nil {
		t.Fatal("protected classification left a hold behind")
	}
}

func TestEmptyURLHoldExpiryDetachesBothConsistently(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	emptyURLAttachPair(t, g, true)
	if g.held["S-TAB"] == nil || g.held["S-PAGE"] == nil {
		t.Fatal("expected both sessions held before expiry")
	}
	_ = g.expireHold("S-PAGE")
	_ = g.expireHold("S-TAB")
	if g.sessions["S-TAB"] != nil || g.sessions["S-PAGE"] != nil {
		t.Fatal("expiry left a dangling client session")
	}
	if g.held["S-TAB"] != nil || g.held["S-PAGE"] != nil {
		t.Fatal("expiry left a hold behind")
	}
	for _, line := range logs {
		if strings.Contains(line, "protected origin") {
			t.Fatalf("unclassified expiry used the protected-origin label: %s", line)
		}
	}
}

func TestEmptyURLHoldPageBeforeTabOrdinaryOrder(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachPair(t, g, false)
	v := g.fromBrowser(emptyURLRelayChanged("PAGE", "page", "https://ordinary.test/popup"))
	ps := emptyURLHoldPayloads(v)
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
	emptyURLAttachPair(t, g, true)
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
	emptyURLAttachPair(t, g, true)
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
	emptyURLAttachPair(t, g, true)
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

func TestEmptyURLHoldMutationAnnounceTabEarly(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLTabAttach(true))
	if emptyURLHoldContains(emptyURLHoldPayloads(v), "attachedToTarget") || g.sessions["S-TAB"] != nil {
		t.Fatal("mutation surface: empty-URL tab was announced early")
	}
}

func TestEmptyURLHoldMutationGuardResumeSkipped(t *testing.T) {
	g := emptyURLHoldGuard(t)
	_ = g.fromBrowser(emptyURLTabAttach(true))
	_ = g.fromBrowser(emptyURLPageAttach(true))
	for _, sid := range []string{"S-TAB", "S-PAGE"} {
		h := g.held[sid]
		if h == nil {
			t.Fatalf("expected %s held", sid)
		}
		if !h.resumed {
			t.Fatalf("mutation surface: %s was not guard-resumed", sid)
		}
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
	for _, sid := range []string{"S-TAB-A", "S-PAGE-A", "S-TAB-B", "S-PAGE-B"} {
		if g.held[sid] == nil {
			t.Fatalf("expected %s held before classification", sid)
		}
		if g.sessions[sid] != nil {
			t.Fatalf("%s admitted before classification", sid)
		}
	}
}

func emptyURLAssertPairStillHeld(t *testing.T, g *guard, tabSession, pageSession string, payloads []string) {
	t.Helper()
	if g.held[tabSession] == nil || g.held[pageSession] == nil {
		t.Fatalf("expected %s/%s still held", tabSession, pageSession)
	}
	if g.sessions[tabSession] != nil || g.sessions[pageSession] != nil {
		t.Fatalf("%s/%s admitted while still unclassified", tabSession, pageSession)
	}
	if emptyURLHoldAttachIndex(payloads, tabSession) >= 0 || emptyURLHoldAttachIndex(payloads, pageSession) >= 0 {
		t.Fatalf("unclassified sibling pair reached the client: %v", payloads)
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
	tabA := emptyURLHoldAttachIndex(ps, "S-TAB-A")
	pageA := emptyURLHoldAttachIndex(ps, "S-PAGE-A")
	if tabA < 0 || pageA < 0 || tabA > pageA {
		t.Fatalf("A ordinary missing tab-then-page: %v", ps)
	}
	if g.sessions["S-TAB-A"] == nil || g.sessions["S-PAGE-A"] == nil {
		t.Fatal("A ordinary did not admit A")
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
	if g.held["S-TAB-B"] != nil || g.held["S-PAGE-B"] != nil {
		t.Fatal("B protected left a hold behind")
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-B") < 0 {
		t.Fatalf("B protected missing consistent detach: %v", sends)
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
	tabB := emptyURLHoldAttachIndex(ps, "S-TAB-B")
	pageB := emptyURLHoldAttachIndex(ps, "S-PAGE-B")
	if tabB < 0 || pageB < 0 {
		t.Fatalf("B ordinary missing tab/page attach: %v", ps)
	}
	if tabB > pageB {
		t.Fatalf("B ordinary announced page before tab: %v", ps)
	}
	if emptyURLHoldAttachCount(ps, "S-TAB-B") != 1 {
		t.Fatalf("B tab announced %d times: %v", emptyURLHoldAttachCount(ps, "S-TAB-B"), ps)
	}
	if emptyURLHoldAttachCount(ps, "S-PAGE-B") != 1 {
		t.Fatalf("B page announced %d times: %v", emptyURLHoldAttachCount(ps, "S-PAGE-B"), ps)
	}
	if g.held["S-TAB-B"] != nil || g.held["S-PAGE-B"] != nil {
		t.Fatal("B ordinary left a hold behind")
	}
	if g.sessions["S-TAB-B"] == nil || g.sessions["S-PAGE-B"] == nil {
		t.Fatal("B ordinary did not admit both sessions")
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
	if emptyURLHoldDetachIndex(sends, "S-TAB-A") < 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-A") < 0 {
		t.Fatalf("A protected missing A detach: %v", sends)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") >= 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-B") >= 0 {
		t.Fatalf("A protected detached unclassified B: %v", sends)
	}
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", append(ps, sends...))
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://ordinary.test/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	tabB := emptyURLHoldAttachIndex(ps, "S-TAB-B")
	pageB := emptyURLHoldAttachIndex(ps, "S-PAGE-B")
	if tabB < 0 || pageB < 0 || tabB > pageB {
		t.Fatalf("B ordinary after A protected missing tab-then-page: %v", ps)
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
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-B") < 0 {
		t.Fatalf("B protected after A protected missing B detach: %v", sends)
	}
}

func TestEmptyURLHoldSiblingOpenerExpiryIndependent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, true)
	_ = g.expireHold("S-PAGE-A")
	if g.held["S-TAB-A"] != nil || g.held["S-PAGE-A"] != nil {
		t.Fatal("expiry of A left A held")
	}
	if g.sessions["S-TAB-A"] != nil || g.sessions["S-PAGE-A"] != nil {
		t.Fatal("expiry of A admitted A")
	}
	if g.held["S-TAB-B"] == nil || g.held["S-PAGE-B"] == nil {
		t.Fatal("expiry of A detached B before B's own HoldTimeout")
	}
	if g.sessions["S-TAB-B"] != nil || g.sessions["S-PAGE-B"] != nil {
		t.Fatal("expiry of A admitted B")
	}
	_ = g.expireHold("S-TAB-B")
	if g.held["S-TAB-B"] != nil || g.held["S-PAGE-B"] != nil {
		t.Fatal("expiry of B left B held")
	}
	if g.sessions["S-TAB-B"] != nil || g.sessions["S-PAGE-B"] != nil {
		t.Fatal("expiry of B admitted B")
	}
}

func TestEmptyURLHoldSiblingOpenerPageBeforeTabIndependent(t *testing.T) {
	g := emptyURLHoldGuard(t)
	emptyURLAttachSiblingPairs(t, g, false)
	if g.held["S-PAGE-A"].parent != "" || g.held["S-PAGE-B"].parent != "" {
		t.Fatal("page-before-tab pairs unexpectedly parent-linked")
	}
	if heldOpenerLocked(g.held["S-PAGE-A"]) != "SHARED" || heldOpenerLocked(g.held["S-TAB-A"]) != "SHARED" {
		t.Fatal("page-before-tab pair A missing shared opener link")
	}
	v := g.fromBrowser(emptyURLNamedRelayChanged("PAGE-A", "page", "https://ordinary.test/a", "SHARED"))
	ps := emptyURLHoldPayloads(v)
	tabA := emptyURLHoldAttachIndex(ps, "S-TAB-A")
	pageA := emptyURLHoldAttachIndex(ps, "S-PAGE-A")
	if tabA < 0 || pageA < 0 || tabA > pageA {
		t.Fatalf("page-before-tab A ordinary missing tab-then-page: %v", ps)
	}
	emptyURLAssertPairStillHeld(t, g, "S-TAB-B", "S-PAGE-B", ps)
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-B", "page", "https://seven.sliccy.ai/b", "SHARED"))
	ps = emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("page-before-tab B protected announced an attach: %v", ps)
	}
	if emptyURLHoldDetachIndex(sends, "S-TAB-B") < 0 || emptyURLHoldDetachIndex(sends, "S-PAGE-B") < 0 {
		t.Fatalf("page-before-tab B protected missing detach: %v", sends)
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

func TestEmptyURLHoldWindowOpenEmptyPageFromOrdinaryOpenerThenClassifies(t *testing.T) {
	g := emptyURLHoldGuard(t)
	var client []string
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-WO", "TAB-WO", "OPENER", true))
	client = append(client, emptyURLHoldPayloads(v)...)
	v = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-WO", "S-PAGE-WO", "PAGE-WO", "OPENER", true))
	client = append(client, emptyURLHoldPayloads(v)...)
	if g.sessions["S-TAB-WO"] == nil {
		t.Fatalf("ordinary-opener tab was not admitted while its page URL was still empty: held=%v client=%v", g.held["S-TAB-WO"] != nil, client)
	}
	v = g.fromBrowser(emptyURLNamedRelayChanged("PAGE-WO", "page", "https://ordinary.test/popup", "OPENER"))
	client = append(client, emptyURLHoldPayloads(v)...)
	emptyURLAssertClientDrivesPage(t, g, client, "S-TAB-WO", "S-PAGE-WO")
}

func TestEmptyURLHoldProtectedOpenerStillHeld(t *testing.T) {
	g := emptyURLHoldGuard(t)
	g.targets["PROT"] = &target{id: "PROT", typ: "page", url: "https://seven.sliccy.ai/", protected: true}
	g.sessions["S-PROT"] = &session{id: "S-PROT", target: "PROT", typ: "page"}
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-P", "TAB-P", "PROT", true))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-TAB-P"] != nil {
		t.Fatalf("protected-opener tab announced before classification: %v", ps)
	}
	v = g.fromBrowser(emptyURLNamedPageAttachURL("S-TAB-P", "S-PAGE-P", "PAGE-P", "PROT", "https://ordinary.test/from-prot", false))
	ps = append(ps, emptyURLHoldPayloads(v)...)
	if emptyURLHoldAttachIndex(ps, "S-PAGE-P") >= 0 || g.sessions["S-PAGE-P"] != nil {
		t.Fatalf("protected-opener page reached the client before its own classification path: %v", ps)
	}
}

func TestEmptyURLHoldUnknownOpenerStaysOut(t *testing.T) {
	var logs []string
	g := emptyURLHoldGuardLog(t, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-U", "TAB-U", "UNKNOWN", true))
	ps := emptyURLHoldPayloads(v)
	sends := emptyURLHoldSends(v)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-TAB-U"] != nil {
		t.Fatalf("unknown-opener tab reached the client: %v", ps)
	}
	if !g.hidden["S-TAB-U"] && emptyURLHoldDetachIndex(sends, "S-TAB-U") < 0 && g.held["S-TAB-U"] == nil {
		t.Fatalf("unknown-opener tab was neither hidden, detached, nor held: sends=%v logs=%v", sends, logs)
	}
	v = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-U", "S-PAGE-U", "PAGE-U", "UNKNOWN", true))
	ps = append(ps, emptyURLHoldPayloads(v)...)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-PAGE-U"] != nil {
		t.Fatalf("unknown-opener page reached the client: %v", ps)
	}
}

func TestEmptyURLHoldEmptyOpenerWithoutCreateStillHeld(t *testing.T) {
	g := emptyURLHoldGuard(t)
	v := g.fromBrowser(emptyURLNamedTabAttach("S-TAB-E", "TAB-E", "", true))
	ps := emptyURLHoldPayloads(v)
	if emptyURLHoldContains(ps, "attachedToTarget") || g.sessions["S-TAB-E"] != nil {
		t.Fatalf("empty-opener tab without createTarget announced early: %v", ps)
	}
	if g.held["S-TAB-E"] == nil {
		t.Fatal("empty-opener tab without createTarget was not held")
	}
	v = g.fromBrowser(emptyURLNamedPageAttach("S-TAB-E", "S-PAGE-E", "PAGE-E", "", true))
	ps = append(ps, emptyURLHoldPayloads(v)...)
	if emptyURLHoldContains(ps, "attachedToTarget") {
		t.Fatalf("empty-opener page without createTarget announced early: %v", ps)
	}
}
