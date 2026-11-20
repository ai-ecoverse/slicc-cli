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
	needle := `"sessionId":"` + sessionID + `"`
	for i, s := range ss {
		if strings.Contains(s, "attachedToTarget") && strings.Contains(s, needle) {
			return i
		}
	}
	return -1
}

func emptyURLTabAttach(waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-TAB",
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": "TAB", "type": "tab", "url": "", "openerId": "OPENER"},
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
			"targetInfo":         map[string]any{"targetId": "PAGE", "type": "page", "url": "", "openerId": "OPENER"},
		},
	})
}

func emptyURLPageAttachFirst(waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-PAGE",
			"waitingForDebugger": waiting,
			"targetInfo":         map[string]any{"targetId": "PAGE", "type": "page", "url": "", "openerId": "OPENER"},
		},
	})
}

func emptyURLRelayChanged(targetID, typ, url string) []byte {
	return encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{"targetId": targetID, "type": typ, "url": url, "openerId": "OPENER"},
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
			"targetInfo":         map[string]any{"targetId": "LEAK", "type": "tab", "url": "", "openerId": "OPENER"},
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
			"targetInfo":         map[string]any{"targetId": "LEAK2", "type": "tab", "url": "", "openerId": "OPENER"},
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
			"targetInfo":         map[string]any{"targetId": "CHILD", "type": "page", "url": "", "openerId": "OPENER"},
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
