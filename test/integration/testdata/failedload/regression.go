package cdp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	failedHostTitle = "seven.sliccy.ai"
	failedProtected = "https://seven.sliccy.ai/missing"
	failedOrdinary  = "https://ordinary.test/missing"
	chromeErrorURL  = "chrome-error://chromewebdata/"
)

func failedLoadGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-OPENER"] = &session{id: "S-OPENER", target: "OPENER", typ: "page"}
	return g
}

func clientPayloads(v browserVerdict) []string {
	var out []string
	if v.forward != nil {
		out = append(out, string(v.forward))
	}
	for _, b := range v.page {
		out = append(out, string(b))
	}
	return out
}

func payloadsContain(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func pageStrings(page [][]byte) []string {
	out := make([]string, 0, len(page))
	for _, b := range page {
		out = append(out, string(b))
	}
	return out
}

func chromeErrorCreated(title, opener string) []byte {
	return encode(map[string]any{
		"method": "Target.targetCreated",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": title, "openerId": opener,
			},
		},
	})
}

func chromeErrorChanged(title, opener string) []byte {
	return encode(map[string]any{
		"method": "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": title, "openerId": opener,
			},
		},
	})
}

func chromeErrorAttached(title, opener string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId": "S-POPUP",
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": title, "openerId": opener,
			},
			"waitingForDebugger": waiting,
		},
	})
}

func emptyAttached(opener string) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-POPUP",
			"targetInfo":         map[string]any{"targetId": "POPUP", "type": "page", "url": "", "title": "", "openerId": opener},
			"waitingForDebugger": true,
		},
	})
}

func relayChromeErrorChanged(title, opener string) []byte {
	return encode(map[string]any{
		"sessionId": "RELAY",
		"method":    "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": title, "openerId": opener,
			},
		},
	})
}

func proveFailedLoad(t *testing.T, g *guard, unreachable string) [][]byte {
	t.Helper()
	nt := g.navTargets["POPUP"]
	if nt == nil {
		nt = &navTarget{target: "POPUP", session: "GUARD", ready: make(chan struct{})}
		g.navTargets["POPUP"] = nt
	}
	if nt.session == "" {
		nt.session = "GUARD"
	}
	if pop := g.targets["POPUP"]; pop != nil && pop.url == "" {
		pop.url = chromeErrorURL
	}
	g.beginConfirmLocked(nt)
	check := nt.confirm
	frame := map[string]any{
		"id": "POPUP", "url": chromeErrorURL, "securityOrigin": "://", "unreachableUrl": unreachable,
	}
	raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": frame}})
	return g.confirmedTargetLocked(nt, check, nt.revision, message{Result: raw})
}

func assertNoFailedHost(t *testing.T, label string, payloads []string) {
	t.Helper()
	if payloadsContain(payloads, failedHostTitle) || payloadsContain(payloads, failedProtected) {
		t.Fatalf("%s leaked failed-load identity: %v", label, payloads)
	}
}

func TestFailedLoadCreatedBlanksUntilProof(t *testing.T) {
	g := failedLoadGuard(t)
	v := g.fromBrowser(chromeErrorCreated(failedHostTitle, "OPENER"))
	ps := clientPayloads(v)
	assertNoFailedHost(t, "targetCreated", ps)
	if !payloadsContain(ps, `"url":""`) && !payloadsContain(ps, `"url": ""`) {
		if len(ps) == 0 {
			return
		}
		t.Fatalf("pending failed-load listing should blank url: %v", ps)
	}
}

func TestFailedLoadAttachedHoldsUntilProof(t *testing.T) {
	g := failedLoadGuard(t)
	v := g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", false))
	ps := clientPayloads(v)
	if payloadsContain(ps, "attachedToTarget") {
		t.Fatalf("attachedToTarget announced before proof: %v", ps)
	}
	assertNoFailedHost(t, "attach-before-proof", ps)
	if g.held["S-POPUP"] == nil && g.sessions["S-POPUP"] != nil {
		t.Fatal("session admitted before proof")
	}
}

func TestFailedLoadEmptyThenRelayChangedHoldsTitle(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(emptyAttached("OPENER"))
	if g.held["S-POPUP"] == nil {
		t.Fatal("empty-url attach was not held")
	}
	v := g.fromBrowser(relayChromeErrorChanged(failedHostTitle, "OPENER"))
	ps := clientPayloads(v)
	if payloadsContain(ps, "attachedToTarget") {
		t.Fatalf("relay change delivered attach before proof: %v", ps)
	}
	assertNoFailedHost(t, "relay-change", ps)
}

func TestFailedLoadEmptyThenNonRelayChangedBlanks(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(emptyAttached("OPENER"))
	v := g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	ps := clientPayloads(v)
	assertNoFailedHost(t, "non-relay-change", ps)
	if payloadsContain(ps, "attachedToTarget") {
		t.Fatalf("non-relay change announced attach: %v", ps)
	}
}

func TestFailedLoadProtectedProofHidesWithoutClientAttach(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, failedProtected)
	page := pageStrings(g.takePendingPageLocked())
	assertNoFailedHost(t, "protected-proof-page", page)
	if payloadsContain(page, "attachedToTarget") && payloadsContain(page, failedHostTitle) {
		t.Fatalf("protected proof announced attach with failed-load title: %v", page)
	}
	v := g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	assertNoFailedHost(t, "protected-listing", clientPayloads(v))
}

func TestFailedLoadOrdinaryProofReleasesRealTitle(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	_ = proveFailedLoad(t, g, failedOrdinary)
	page := pageStrings(g.takePendingPageLocked())
	if g.targetProtectedLocked("POPUP") {
		t.Fatal("ordinary failed load was protected")
	}
	if g.sessions["S-POPUP"] == nil {
		t.Fatal("ordinary failed load stayed unattachable")
	}
	if !payloadsContain(page, "attachedToTarget") {
		t.Fatalf("ordinary proof did not announce attach: %v", page)
	}
	if !payloadsContain(page, `"title":"ordinary.test"`) && !payloadsContain(page, `"title": "ordinary.test"`) {
		t.Fatalf("ordinary proof lost the real title: %v", page)
	}
}

func TestFailedLoadUnreadableProofStaysHeld(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	if pop := g.targets["POPUP"]; pop != nil {
		pop.url = chromeErrorURL
	}
	nt := &navTarget{target: "POPUP", session: "GUARD", ready: make(chan struct{})}
	g.navTargets["POPUP"] = nt
	g.beginConfirmLocked(nt)
	check := nt.confirm
	_ = g.confirmedTargetLocked(nt, check, nt.revision, message{Error: json.RawMessage(`{"message":"boom"}`)})
	if g.sessions["S-POPUP"] != nil {
		t.Fatal("unreadable proof admitted a client session")
	}
	v := g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	assertNoFailedHost(t, "unreadable-listing", clientPayloads(v))
}

func TestFailedLoadDetachWhilePendingLeavesNoClientAttach(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	v := g.fromBrowser([]byte(`{"method":"Target.detachedFromTarget","params":{"sessionId":"S-POPUP","targetId":"POPUP"}}`))
	ps := clientPayloads(v)
	if payloadsContain(ps, "attachedToTarget") || payloadsContain(ps, "detachedFromTarget") {
		t.Fatalf("pending detach raced into client attach pairing: %v", ps)
	}
	if g.sessions["S-POPUP"] != nil || g.held["S-POPUP"] != nil {
		t.Fatal("pending session remained after chrome detach")
	}
}

func TestFailedLoadGetTargetsBlanksPending(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorCreated(failedHostTitle, "OPENER"))
	raw, _ := json.Marshal(map[string]any{
		"id": 1,
		"result": map[string]any{
			"targetInfos": []any{
				map[string]any{"targetId": "OPENER", "type": "page", "url": "https://ordinary.test/", "title": "ok"},
				map[string]any{"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": failedHostTitle, "openerId": "OPENER"},
			},
		},
	})
	g.pending[callKey{"", 1}] = kindTargets
	v := g.fromBrowser(raw)
	if v.forward == nil {
		t.Fatal("getTargets reply dropped")
	}
	s := string(v.forward)
	if strings.Contains(s, failedHostTitle) {
		t.Fatalf("getTargets carried failed-load title: %s", s)
	}
}

func TestFailedLoadAttachEventBeforeConfirmReply(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	if g.held["S-POPUP"] == nil {
		t.Fatal("attach before confirm was not held")
	}
	if g.sessions["S-POPUP"] != nil {
		t.Fatal("client session admitted before confirm")
	}
	_ = proveFailedLoad(t, g, failedOrdinary)
	page := pageStrings(g.takePendingPageLocked())
	if g.sessions["S-POPUP"] == nil {
		t.Fatal("ordinary proof left target unattachable")
	}
	if !payloadsContain(page, "attachedToTarget") {
		t.Fatalf("attach before confirm never released: %v", page)
	}
}

func TestFailedLoadConfirmReplyBeforeAttachEvent(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorCreated("ordinary.test", "OPENER"))
	if pop := g.targets["POPUP"]; pop == nil {
		t.Fatal("missing popup target")
	}
	_ = proveFailedLoad(t, g, failedOrdinary)
	v := g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", false))
	ps := clientPayloads(v)
	if g.targetProtectedLocked("POPUP") {
		t.Fatal("ordinary proof was protected")
	}
	if g.sessions["S-POPUP"] == nil && !payloadsContain(ps, "attachedToTarget") {
		t.Fatalf("confirm-before-attach blocked ordinary attach: sessions=%v payloads=%v", g.sessions["S-POPUP"], ps)
	}
	assertNoFailedHost(t, "confirm-before-attach", ps)
}

func reattachChromeError(title string, waiting bool) []byte {
	return encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId": "S-POPUP-2",
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": title, "openerId": "OPENER",
			},
			"waitingForDebugger": waiting,
		},
	})
}

func frameNavigatedChromeError(unreachable string) []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{
				"id": "POPUP", "url": chromeErrorURL, "securityOrigin": "://", "unreachableUrl": unreachable,
			},
		},
	})
}

func TestFailedLoadOrdinaryThenProtectedBlanks(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	_ = proveFailedLoad(t, g, failedOrdinary)
	_ = g.takePendingPageLocked()
	v := g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	assertNoFailedHost(t, "ordinary-then-protected-listing", clientPayloads(v))
	_ = g.fromBrowser(reattachChromeError(failedHostTitle, true))
	if g.sessions["S-POPUP-2"] != nil {
		t.Fatal("second failed load admitted a client session before proof")
	}
	ps := clientPayloads(g.fromBrowser(nil))
	assertNoFailedHost(t, "ordinary-then-protected-hold", ps)
	_ = proveFailedLoad(t, g, failedProtected)
	page := pageStrings(g.takePendingPageLocked())
	assertNoFailedHost(t, "ordinary-then-protected-proof", page)
	v = g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	assertNoFailedHost(t, "ordinary-then-protected-after", clientPayloads(v))
}

func TestFailedLoadProtectedThenOrdinaryReleases(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, failedProtected)
	_ = g.takePendingPageLocked()
	v := g.fromBrowser(chromeErrorChanged("ordinary.test", "OPENER"))
	ps := clientPayloads(v)
	if payloadsContain(ps, failedHostTitle) {
		t.Fatalf("protected-then-ordinary listing kept prior title: %v", ps)
	}
	_ = g.fromBrowser(reattachChromeError("ordinary.test", true))
	_ = proveFailedLoad(t, g, failedOrdinary)
	page := pageStrings(g.takePendingPageLocked())
	if !payloadsContain(page, "attachedToTarget") {
		t.Fatalf("second ordinary proof did not announce attach: %v", page)
	}
	if !payloadsContain(page, `"title":"ordinary.test"`) && !payloadsContain(page, `"title": "ordinary.test"`) {
		t.Fatalf("second ordinary proof lost the real title: %v", page)
	}
	assertNoFailedHost(t, "protected-then-ordinary", page)
}

func TestFailedLoadTwoFailedLoadsBackToBack(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	_ = proveFailedLoad(t, g, failedOrdinary)
	_ = g.takePendingPageLocked()
	v := g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	assertNoFailedHost(t, "back-to-back-first-listing", clientPayloads(v))
	_ = g.fromBrowser(reattachChromeError(failedHostTitle, true))
	_ = proveFailedLoad(t, g, failedProtected)
	assertNoFailedHost(t, "back-to-back-protected", pageStrings(g.takePendingPageLocked()))
	v = g.fromBrowser(chromeErrorChanged("ordinary2.test", "OPENER"))
	if payloadsContain(clientPayloads(v), failedHostTitle) {
		t.Fatalf("back-to-back third listing kept prior title: %v", clientPayloads(v))
	}
	_ = g.fromBrowser(reattachChromeError("ordinary2.test", true))
	_ = proveFailedLoad(t, g, "https://ordinary2.test/missing")
	page := pageStrings(g.takePendingPageLocked())
	if !payloadsContain(page, `"title":"ordinary2.test"`) && !payloadsContain(page, `"title": "ordinary2.test"`) {
		t.Fatalf("third failed load lost its title: %v", page)
	}
	assertNoFailedHost(t, "back-to-back-third", page)
}

func TestFailedLoadStaleProofDoesNotReleaseNewDocument(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	nt := g.navTargets["POPUP"]
	if nt == nil {
		nt = &navTarget{target: "POPUP", session: "GUARD", ready: make(chan struct{})}
		g.navTargets["POPUP"] = nt
	}
	nt.session = "GUARD"
	g.beginConfirmLocked(nt)
	stale := nt.confirm
	staleRev := nt.revision
	_ = g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	_ = g.fromBrowser(frameNavigatedChromeError(failedProtected))
	assertNoFailedHost(t, "stale-after-nav-listing", clientPayloads(g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))))
	frame := map[string]any{
		"id": "POPUP", "url": chromeErrorURL, "securityOrigin": "://", "unreachableUrl": failedOrdinary,
	}
	raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": frame}})
	_ = g.confirmedTargetLocked(nt, stale, staleRev, message{Result: raw})
	page := pageStrings(g.takePendingPageLocked())
	if payloadsContain(page, "ordinary.test") {
		t.Fatalf("stale ordinary proof released the new document: %v", page)
	}
	assertNoFailedHost(t, "stale-proof", page)
	if g.sessions["S-POPUP"] != nil && payloadsContain(page, "attachedToTarget") {
		t.Fatalf("stale proof admitted attach for the new document: %v", page)
	}
}
