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
	if g.sessions["S-POPUP"] != nil {
		t.Fatalf("protected proof admitted a client session: page=%v", page)
	}
	if payloadsContain(page, "attachedToTarget") {
		t.Fatalf("protected proof announced attach: %v", page)
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

func assertFailedLoadNotOrdinary(t *testing.T, label, unreachable string, g *guard) {
	t.Helper()
	st := g.errorProofOfLocked("POPUP")
	if st == errorProofOrdinary {
		t.Fatalf("%s: unreachable %q released as ordinary", label, unreachable)
	}
	page := pageStrings(g.takePendingPageLocked())
	if payloadsContain(page, `"title":"`+failedHostTitle+`"`) || payloadsContain(page, `"title": "`+failedHostTitle+`"`) {
		t.Fatalf("%s: restored stored title for unreachable %q: %v", label, unreachable, page)
	}
	assertNoFailedHost(t, label, page)
}

func TestFailedLoadVerdictUserinfoProtectedAtOrdinaryHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	u := "https://seven.sliccy.ai@ordinary.test/missing"
	_ = proveFailedLoad(t, g, u)
	assertFailedLoadNotOrdinary(t, "userinfo-protected-at-ordinary", u, g)
}

func TestFailedLoadVerdictAboutBlankHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, "about:blank")
	assertFailedLoadNotOrdinary(t, "about-blank", "about:blank", g)
}

func TestFailedLoadVerdictAboutSrcdocHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, "about:srcdoc")
	assertFailedLoadNotOrdinary(t, "about-srcdoc", "about:srcdoc", g)
}

func TestFailedLoadVerdictDataURLHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, "data:text/html,hi")
	assertFailedLoadNotOrdinary(t, "data-url", "data:text/html,hi", g)
}

func TestFailedLoadVerdictJavascriptURLHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, "javascript:alert(1)")
	assertFailedLoadNotOrdinary(t, "javascript-url", "javascript:alert(1)", g)
}

func TestFailedLoadVerdictEmptyUnreachableHides(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, "")
	assertFailedLoadNotOrdinary(t, "empty-unreachable", "", g)
}

func TestFailedLoadVerdictControlFormsStayHidden(t *testing.T) {
	controls := []struct {
		name string
		u    string
	}{
		{"plain-protected", failedProtected},
		{"blob-wrap", "blob:https://seven.sliccy.ai/uuid"},
		{"filesystem-wrap", "filesystem:https://seven.sliccy.ai/temporary/x"},
		{"view-source-wrap", "view-source:https://seven.sliccy.ai/missing"},
		{"userinfo-ordinary-at-protected", "https://ordinary@seven.sliccy.ai/missing"},
		{"encoded-at-hostish", "https://seven.sliccy.ai%40ordinary.test/missing"},
		{"uppercase-host", "HTTPS://SEVEN.SLICCY.AI/missing"},
		{"trailing-dot-host", "https://seven.sliccy.ai./missing"},
		{"default-https-port", "https://seven.sliccy.ai:443/missing"},
		{"nondefault-port", "https://seven.sliccy.ai:80/missing"},
		{"http-scheme", "http://seven.sliccy.ai/missing"},
	}
	for _, c := range controls {
		t.Run(c.name, func(t *testing.T) {
			g := failedLoadGuard(t)
			_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
			_ = proveFailedLoad(t, g, c.u)
			assertFailedLoadNotOrdinary(t, c.name, c.u, g)
		})
	}
}

func TestFailedLoadStaleErrorReplyKeepsNewHold(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	nt := g.navTargets["POPUP"]
	if nt == nil {
		nt = &navTarget{target: "POPUP", session: "GUARD", ready: make(chan struct{})}
		g.navTargets["POPUP"] = nt
	}
	nt.session = "GUARD"
	g.beginConfirmLocked(nt)
	check := nt.confirm
	rev := nt.revision
	genAtConfirm := check.failedLoadGen
	if g.held["S-POPUP"] == nil {
		t.Fatal("expected held session")
	}
	g.errorProofGen["POPUP"] = genAtConfirm + 1
	g.setErrorProofLocked("POPUP", errorProofPending)
	_ = g.confirmedTargetLocked(nt, check, rev, message{Error: json.RawMessage(`{"message":"stale"}`)})
	if g.errorProofOfLocked("POPUP") == errorProofFailed && g.held["S-POPUP"] == nil {
		t.Fatalf("stale error reply hid the hold despite generation bump: state=%d", g.errorProofOfLocked("POPUP"))
	}
}

func chromeErrorChangedURL(title, url, opener string) []byte {
	return encode(map[string]any{
		"method": "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId": "POPUP", "type": "page", "url": url, "title": title, "openerId": opener,
			},
		},
	})
}

func frameNavigatedChromeErrorTyped(unreachable, navType string) []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"type": navType,
			"frame": map[string]any{
				"id": "POPUP", "url": chromeErrorURL, "securityOrigin": "://", "unreachableUrl": unreachable,
			},
		},
	})
}

func TestFailedLoadBfcacheRestoreResetsProof(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorCreated("ordinary.test", "OPENER"))
	delete(g.navTargets, "POPUP")
	g.setErrorProofLocked("POPUP", errorProofOrdinary)
	g.errorTitles["POPUP"] = "ordinary.test"
	gen0 := g.errorProofGenOfLocked("POPUP")
	g.sessions["GUARD"] = &session{id: "GUARD", target: "POPUP", typ: "page"}
	_ = g.fromBrowser(frameNavigatedChromeErrorTyped(failedProtected, "BackForwardCacheRestore"))
	if g.errorProofOfLocked("POPUP") == errorProofOrdinary && g.errorProofGenOfLocked("POPUP") == gen0 {
		t.Fatalf("bfcache restore left settled ordinary proof in place: state=%d gen=%d", g.errorProofOfLocked("POPUP"), g.errorProofGenOfLocked("POPUP"))
	}
}

func TestFailedLoadSameURLReloadResetsProof(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorCreated(failedHostTitle, "OPENER"))
	delete(g.navTargets, "POPUP")
	g.setErrorProofLocked("POPUP", errorProofProtected)
	g.errorTitles["POPUP"] = failedHostTitle
	gen0 := g.errorProofGenOfLocked("POPUP")
	g.sessions["GUARD"] = &session{id: "GUARD", target: "POPUP", typ: "page"}
	_ = g.fromBrowser(frameNavigatedChromeErrorTyped(failedProtected, ""))
	if g.errorProofOfLocked("POPUP") == errorProofProtected && g.errorProofGenOfLocked("POPUP") == gen0 {
		t.Fatalf("same-url reload left settled protected proof in place: state=%d gen=%d", g.errorProofOfLocked("POPUP"), g.errorProofGenOfLocked("POPUP"))
	}
}

func TestFailedLoadHistoryRestoreResetsProof(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorCreated("ordinary.test", "OPENER"))
	delete(g.navTargets, "POPUP")
	g.setErrorProofLocked("POPUP", errorProofOrdinary)
	g.errorTitles["POPUP"] = "ordinary.test"
	gen0 := g.errorProofGenOfLocked("POPUP")
	g.sessions["GUARD"] = &session{id: "GUARD", target: "POPUP", typ: "page"}
	_ = g.fromBrowser(frameNavigatedChromeErrorTyped(failedOrdinary, "Navigation"))
	if g.errorProofOfLocked("POPUP") == errorProofOrdinary && g.errorProofGenOfLocked("POPUP") == gen0 {
		t.Fatalf("history restore left settled ordinary proof in place: state=%d gen=%d", g.errorProofOfLocked("POPUP"), g.errorProofGenOfLocked("POPUP"))
	}
}

func TestFailedLoadDestroyClearsProofMaps(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached(failedHostTitle, "OPENER", true))
	_ = proveFailedLoad(t, g, failedProtected)
	_ = g.takePendingPageLocked()
	if g.errorProofOfLocked("POPUP") == errorProofNone {
		t.Fatal("expected settled proof before destroy")
	}
	g.errorTitles["POPUP"] = failedHostTitle
	g.destroyedLocked("POPUP")
	if g.errorProofOfLocked("POPUP") != errorProofNone {
		t.Fatalf("destroy left errorProof state=%d", g.errorProofOfLocked("POPUP"))
	}
	if g.errorProofGenOfLocked("POPUP") != 0 {
		t.Fatalf("destroy left errorProofGen=%d", g.errorProofGenOfLocked("POPUP"))
	}
	if g.errorTitles["POPUP"] != "" {
		t.Fatalf("destroy left errorTitles=%q", g.errorTitles["POPUP"])
	}
}

func TestFailedLoadNilHeldDoesNotPanic(t *testing.T) {
	g := failedLoadGuard(t)
	_ = g.fromBrowser(chromeErrorAttached("ordinary.test", "OPENER", true))
	g.held["nil-slot"] = nil
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil held entry panicked: %v", r)
		}
	}()
	_ = proveFailedLoad(t, g, failedOrdinary)
}

func assertClientSeesOnlyFailedOrigin(t *testing.T, label string, raw []byte, failedFull string) {
	t.Helper()
	s := string(raw)
	if strings.Contains(s, "/missing") || strings.Contains(s, "?q=") || strings.Contains(s, "#frag") || strings.Contains(s, "secret") {
		t.Fatalf("%s carried more than the failed origin: %s", label, s)
	}
	if strings.Contains(s, `"title":"`+failedHostTitle+`"`) || strings.Contains(s, `"title": "`+failedHostTitle+`"`) || strings.Contains(s, `"name":"`+failedHostTitle+`"`) || strings.Contains(s, `"name": "`+failedHostTitle+`"`) {
		t.Fatalf("%s carried a failed-load title/name: %s", label, s)
	}
	_ = failedFull
}

func attachedFailedLoadGuard(t *testing.T, failedFull string, protected bool) *guard {
	t.Helper()
	g := failedLoadGuard(t)
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId":          "S-POPUP",
			"targetInfo":         map[string]any{"targetId": "POPUP", "type": "page", "url": "https://ordinary.test/", "title": "ok", "openerId": "OPENER"},
			"waitingForDebugger": false,
		},
	}))
	if g.sessions["S-POPUP"] == nil {
		t.Fatal("ordinary attach missing")
	}
	if g.navTargets["POPUP"] == nil {
		nt := &navTarget{target: "POPUP", session: "GUARD", ready: make(chan struct{})}
		g.navTargets["POPUP"] = nt
		g.navSessions["GUARD"] = nt
	}
	_ = g.fromBrowser(chromeErrorChanged(failedHostTitle, "OPENER"))
	if pop := g.targets["POPUP"]; pop != nil {
		pop.url = chromeErrorURL
	}
	nt := g.navTargets["POPUP"]
	_ = g.fromBrowser(encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{"id": "POPUP", "url": chromeErrorURL, "securityOrigin": "://", "unreachableUrl": failedFull},
		},
	}))
	if protected {
		g.setErrorProofLocked("POPUP", errorProofProtected)
	} else {
		g.setErrorProofLocked("POPUP", errorProofPending)
	}
	g.errorTitles["POPUP"] = failedHostTitle
	if nt.errorURL == "" {
		nt.errorURL = failedFull
	}
	if len(nt.failedURLs) == 0 {
		nt.failedURLs = []string{failedFull}
	}
	return g
}

func TestFailedLoadAttachedSessionPathsStayAtOrigin(t *testing.T) {
	failedFull := "https://seven.sliccy.ai/missing?q=secret#frag"
	type row struct {
		name string
		via  string
		raw  func(g *guard) []byte
	}
	rows := []row{
		{"Page.frameNavigated", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Page.frameNavigated", "params": map[string]any{
				"frame": map[string]any{"id": "POPUP", "url": chromeErrorURL, "name": failedHostTitle, "securityOrigin": "https://seven.sliccy.ai", "unreachableUrl": failedFull},
			}})
		}},
		{"Page.frameStartedNavigating", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Page.frameStartedNavigating", "params": map[string]any{
				"frameId": "POPUP", "url": failedFull,
			}})
		}},
		{"Page.getFrameTree", "scrub", func(g *guard) []byte {
			raw, _ := json.Marshal(map[string]any{"sessionId": "S-POPUP", "id": 21, "result": map[string]any{
				"frameTree": map[string]any{"frame": map[string]any{"id": "POPUP", "url": chromeErrorURL, "unreachableUrl": failedFull}},
			}})
			return raw
		}},
		{"Page.getNavigationHistory", "scrub", func(g *guard) []byte {
			raw, _ := json.Marshal(map[string]any{"sessionId": "S-POPUP", "id": 22, "result": map[string]any{
				"currentIndex": 0,
				"entries":      []any{map[string]any{"id": 1, "url": failedFull, "userTypedURL": failedFull, "title": failedHostTitle}},
			}})
			return raw
		}},
		{"Target.targetInfoChanged", "fromBrowser", func(g *guard) []byte {
			return chromeErrorChanged(failedHostTitle, "OPENER")
		}},
		{"Target.getTargetInfo", "fromBrowser", func(g *guard) []byte {
			g.pending[callKey{"", 23}] = kindTargetInfo
			raw, _ := json.Marshal(map[string]any{"id": 23, "result": map[string]any{
				"targetInfo": map[string]any{"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": failedHostTitle},
			}})
			return raw
		}},
		{"Target.getTargets", "fromBrowser", func(g *guard) []byte {
			g.pending[callKey{"", 24}] = kindTargets
			raw, _ := json.Marshal(map[string]any{"id": 24, "result": map[string]any{
				"targetInfos": []any{map[string]any{"targetId": "POPUP", "type": "page", "url": chromeErrorURL, "title": failedHostTitle}},
			}})
			return raw
		}},
		{"DOM.getDocument", "scrub", func(g *guard) []byte {
			raw, _ := json.Marshal(map[string]any{"sessionId": "S-POPUP", "id": 25, "result": map[string]any{
				"root": map[string]any{"nodeId": 1, "documentURL": failedFull, "baseURL": failedFull},
			}})
			return raw
		}},
		{"Runtime.evaluate", "scrub", func(g *guard) []byte {
			raw, _ := json.Marshal(map[string]any{"sessionId": "S-POPUP", "id": 26, "result": map[string]any{
				"result": map[string]any{"type": "string", "value": failedFull},
			}})
			return raw
		}},
		{"Network.requestWillBeSent", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Network.requestWillBeSent", "params": map[string]any{
				"requestId": "r1", "documentURL": failedFull, "request": map[string]any{"url": failedFull},
			}})
		}},
		{"Network.responseReceivedExtraInfo", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Network.responseReceivedExtraInfo", "params": map[string]any{
				"requestId": "r1", "headers": map[string]any{"location": failedFull},
			}})
		}},
		{"Network.loadingFailed", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Network.loadingFailed", "params": map[string]any{
				"requestId": "r1", "errorText": "net::ERR_NAME_NOT_RESOLVED", "type": "Document",
			}})
		}},
		{"Page.getResourceTree", "scrub", func(g *guard) []byte {
			raw, _ := json.Marshal(map[string]any{"sessionId": "S-POPUP", "id": 27, "result": map[string]any{
				"frameTree": map[string]any{"frame": map[string]any{"id": "POPUP", "url": chromeErrorURL, "unreachableUrl": failedFull}, "resources": []any{map[string]any{"url": failedFull}}},
			}})
			return raw
		}},
		{"Runtime.consoleAPICalled", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Runtime.consoleAPICalled", "params": map[string]any{
				"type": "log", "args": []any{map[string]any{"type": "string", "value": "go " + failedFull}},
			}})
		}},
		{"Log.entryAdded", "scrub", func(g *guard) []byte {
			return encode(map[string]any{"sessionId": "S-POPUP", "method": "Log.entryAdded", "params": map[string]any{
				"entry": map[string]any{"text": "Navigate to " + failedFull, "url": failedFull},
			}})
		}},
	}
	for _, phase := range []string{"pending", "protected"} {
		for _, r := range rows {
			name := phase + "/" + r.name
			t.Run(name, func(t *testing.T) {
				g := attachedFailedLoadGuard(t, failedFull, phase == "protected")
				raw := r.raw(g)
				if r.via == "fromBrowser" {
					v := g.fromBrowser(raw)
					if v.forward != nil {
						assertClientSeesOnlyFailedOrigin(t, name, g.scrub(append([]byte(nil), v.forward...)), failedFull)
						return
					}
					for _, p := range v.page {
						assertClientSeesOnlyFailedOrigin(t, name, g.scrub(append([]byte(nil), p...)), failedFull)
					}
					return
				}
				assertClientSeesOnlyFailedOrigin(t, name, g.scrub(append([]byte(nil), raw...)), failedFull)
			})
		}
	}
}
