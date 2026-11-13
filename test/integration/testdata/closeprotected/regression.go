package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func closeProtectedGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	return g
}

func admitAttach(t *testing.T, g *guard, id int64, target string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": "Target.attachToTarget", "params": map[string]any{"targetId": target, "flatten": true}})
	c, n, ok := parseCommand(raw)
	if !ok || n != id {
		t.Fatalf("parse attach: ok=%t id=%d", ok, n)
	}
	v := g.fromCommand(c, n, ok)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("attach was not admitted: reply=%s forward=%s", v.reply, v.forward)
	}
}

func admitGetTargets(t *testing.T, g *guard, id int64) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": "Target.getTargets"})
	c, n, ok := parseCommand(raw)
	if !ok || n != id {
		t.Fatalf("parse getTargets: ok=%t id=%d", ok, n)
	}
	v := g.fromCommand(c, n, ok)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("getTargets was not admitted: reply=%s forward=%s", v.reply, v.forward)
	}
}

func attachReply(id int64, session string, errMsg string) []byte {
	if errMsg != "" {
		return encode(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": errMsg}})
	}
	return encode(map[string]any{"id": id, "result": map[string]any{"sessionId": session}})
}

func popupCreated() []byte {
	return []byte(`{"method":"Target.targetCreated","params":{"targetInfo":{"targetId":"POPUP","type":"page","url":"https://seven.sliccy.ai/frame","openerId":"OPENER"}}}`)
}

func TestCloseProtectedOverlappingAttachKeepsReservation(t *testing.T) {
	g := closeProtectedGuard(t)
	admitAttach(t, g, 1, "OPENER")
	admitAttach(t, g, 2, "OPENER")
	_ = g.fromBrowser(popupCreated())
	fail := g.fromBrowser(attachReply(1, "", "Target closed"))
	if fail.forward == nil {
		t.Fatal("failed attach reply dropped")
	}
	ok := g.fromBrowser(attachReply(2, "S-OPENER", ""))
	if ok.forward == nil || strings.Contains(string(ok.forward), `"error"`) {
		t.Fatalf("successful attach: %s", ok.forward)
	}
	g.mu.Lock()
	closable := g.streamClosableLocked("POPUP")
	reason := g.closeTargetReasonLocked("POPUP")
	g.mu.Unlock()
	if !closable || reason != "" {
		t.Fatalf("popup not closable after overlapping attach: closable=%t reason=%q opened=%v touched=%v wait=%v ineligible=%v", closable, reason, g.opened["POPUP"], g.touchedAt["OPENER"], g.openedWait["POPUP"], g.openedIneligible["POPUP"])
	}
}

func TestCloseProtectedGetTargetsPreservesFirstSight(t *testing.T) {
	g := closeProtectedGuard(t)
	admitAttach(t, g, 1, "OPENER")
	_ = g.fromBrowser(popupCreated())
	admitGetTargets(t, g, 2)
	list := g.fromBrowser(encode(map[string]any{
		"id": 2,
		"result": map[string]any{
			"targetInfos": []any{
				map[string]any{"targetId": "OPENER", "type": "page", "url": "https://ordinary.test/"},
				map[string]any{"targetId": "POPUP", "type": "page", "url": "https://seven.sliccy.ai/frame", "openerId": "OPENER"},
			},
		},
	}))
	if list.forward == nil {
		t.Fatal("getTargets reply dropped")
	}
	_ = g.fromBrowser(attachReply(1, "S-OPENER", ""))
	g.mu.Lock()
	closable := g.streamClosableLocked("POPUP")
	reason := g.closeTargetReasonLocked("POPUP")
	seen := g.openedSeenAt["POPUP"]
	opened := g.opened["POPUP"]
	wait := g.openedWait["POPUP"]
	g.mu.Unlock()
	if !closable || reason != "" {
		t.Fatalf("popup not closable after getTargets between sight and attach: closable=%t reason=%q seenAt=%d opened=%v wait=%v", closable, reason, seen, opened, wait)
	}
}

func TestCloseProtectedLiveTouchSurvivesFlood(t *testing.T) {
	g := closeProtectedGuard(t)
	g.markTouchedLocked("OPENER")
	for i := 0; i < maxTombstones; i++ {
		id := fmt.Sprintf("T%d", i)
		g.targets[id] = &target{id: id, typ: "page", url: "https://ordinary.test/", gone: true}
		g.touchSeq++
		g.touchedAt[id] = g.touchSeq
		g.touchedOrder = append(g.touchedOrder, id)
	}
	g.trimTouchedLocked()
	if _, ok := g.touchedAt["OPENER"]; !ok {
		t.Fatal("live opener touch was evicted")
	}
	_ = g.fromBrowser(popupCreated())
	g.mu.Lock()
	closable := g.streamClosableLocked("POPUP")
	reason := g.closeTargetReasonLocked("POPUP")
	g.mu.Unlock()
	if !closable || reason != "" {
		t.Fatalf("popup from a live opener was not closable after a touch flood: closable=%t reason=%q", closable, reason)
	}
}

func TestCloseProtectedTrimBeforeStoreKeepsTouchSeq(t *testing.T) {
	g := closeProtectedGuard(t)
	for i := 0; i < maxTombstones; i++ {
		id := fmt.Sprintf("T%d", i)
		g.targets[id] = &target{id: id, typ: "page", url: "https://ordinary.test/", gone: true}
		g.touchSeq++
		g.touchedAt[id] = g.touchSeq
		g.touchedOrder = append(g.touchedOrder, id)
	}
	_ = g.fromBrowser(popupCreated())
	g.mu.Lock()
	if g.openedWait["POPUP"] != "OPENER" {
		t.Fatalf("popup was not waiting on opener touch: wait=%q opened=%v ineligible=%v", g.openedWait["POPUP"], g.opened["POPUP"], g.openedIneligible["POPUP"])
	}
	seenAt := g.openedSeenAt["POPUP"]
	g.targets["OPENER"].gone = true
	g.markTouchedLocked("OPENER")
	touch, ok := g.touchedAt["OPENER"]
	opened := g.opened["POPUP"]
	ineligible := g.openedIneligible["POPUP"]
	wait := g.openedWait["POPUP"]
	g.mu.Unlock()
	if !ok || touch == 0 {
		t.Fatalf("opener touch was lost to trim before recheck: ok=%t touch=%d", ok, touch)
	}
	if opened || wait != "" || !ineligible {
		t.Fatalf("lost touch seq made a pre-touch popup closable: opened=%v wait=%q ineligible=%v seenAt=%d touch=%d", opened, wait, ineligible, seenAt, touch)
	}
	if touch <= seenAt {
		t.Fatalf("expected opener touch after popup sight: seenAt=%d touch=%d", seenAt, touch)
	}
}

func TestCloseProtectedPendingTouchClearedOnSessionDetach(t *testing.T) {
	g := closeProtectedGuard(t)
	g.sessions["S-page"] = &session{id: "S-page", target: "OPENER", typ: "page"}
	g.navTargets["OPENER"] = &navTarget{target: "OPENER", done: true}
	raw, _ := json.Marshal(map[string]any{"id": 1, "sessionId": "S-page", "method": "Target.attachToTarget", "params": map[string]any{"targetId": "OPENER", "flatten": true}})
	c, id, ok := parseCommand(raw)
	if !ok {
		t.Fatal("parse attach")
	}
	v := g.fromCommand(c, id, ok)
	if v.forward == nil {
		t.Fatalf("attach not forwarded: %s", v.reply)
	}
	g.mu.Lock()
	if len(g.pendingTouch) == 0 {
		t.Fatal("expected a pending touch reservation")
	}
	reserved := g.touchSeq
	g.forgetSessionLocked("S-page")
	if len(g.pendingTouch) != 0 {
		t.Fatalf("pendingTouch survived session detach: %+v", g.pendingTouch)
	}
	g.sessions["S-new"] = &session{id: "S-new", target: "OPENER", typ: "page"}
	g.mu.Unlock()
	_ = g.fromBrowser(popupCreated())
	g.mu.Lock()
	closable := g.streamClosableLocked("POPUP")
	reason := g.closeTargetReasonLocked("POPUP")
	pending, _ := g.pendingTouchSeqLocked("OPENER")
	g.mu.Unlock()
	if closable || reason == "" {
		t.Fatalf("popup inherited a detached session's touch reservation: closable=%t reason=%q reserved=%d pending=%d", closable, reason, reserved, pending)
	}
}

func TestCloseProtectedKeepsLiveSevenUIAndVault(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["OS"] = &target{id: "OS", typ: "page", url: "https://seven.sliccy.ai/os/", protected: true}
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/unlock", protected: true}
	g.targets["ERR"] = &target{id: "ERR", typ: "page", url: "https://vault.sliccy.ai/missing", protected: true}
	g.navTargets["OS"] = &navTarget{target: "OS", ready: make(chan struct{}), done: true}
	g.navTargets["VAULT"] = &navTarget{target: "VAULT", ready: make(chan struct{}), done: true}
	g.navTargets["ERR"] = &navTarget{target: "ERR", ready: make(chan struct{}), done: true, documentURL: "chrome-error://chromewebdata/", errorURL: "https://vault.sliccy.ai/missing"}
	close(g.navTargets["OS"].ready)
	close(g.navTargets["VAULT"].ready)
	close(g.navTargets["ERR"].ready)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("OS") == "" {
		t.Fatal("stream-touched live /os/ tab was closable")
	}
	if g.closeTargetReasonLocked("VAULT") == "" {
		t.Fatal("stream-touched vault tab was closable")
	}
	if g.closeTargetReasonLocked("ERR") != "" {
		t.Fatal("stream-touched committed error page was not closable")
	}
}

func TestCloseProtectedKeepsViewSourceBlobUserinfoAndBlank(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["VS"] = &target{id: "VS", typ: "page", url: "view-source:https://vault.sliccy.ai/doc", protected: true}
	g.targets["BLOB"] = &target{id: "BLOB", typ: "page", url: "blob:https://vault.sliccy.ai/0b9e0f6c-1f0e-4c1a-9a43-5d3f6e2c7a10", protected: true}
	g.targets["USER"] = &target{id: "USER", typ: "page", url: "https://user:pass@seven.sliccy.ai/os/index.html", protected: true}
	g.targets["PARENT"] = &target{id: "PARENT", typ: "page", url: "https://vault.sliccy.ai/", protected: true}
	g.targets["BLANK"] = &target{id: "BLANK", typ: "page", url: "about:blank", opener: "PARENT", protected: true}
	g.targets["NAV"] = &target{id: "NAV", typ: "page", url: "https://ordinary.test/", protected: true}
	for _, id := range []string{"VS", "BLOB", "USER", "BLANK", "NAV"} {
		g.navTargets[id] = &navTarget{target: id, ready: make(chan struct{}), done: true}
		close(g.navTargets[id].ready)
	}
	g.navTargets["BLANK"].documentURL = "about:blank"
	g.navTargets["BLANK"].securityOrigin = "https://vault.sliccy.ai"
	g.navTargets["BLANK"].keptOrigin = "https://vault.sliccy.ai"
	g.keptURLLatch["BLANK"] = true
	g.navTargets["NAV"].unsure = true
	g.navTargets["NAV"].navigationURL = "https://vault.sliccy.ai/slow"
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range []string{"VS", "BLOB", "USER", "BLANK", "NAV"} {
		if g.closeTargetReasonLocked(id) == "" {
			t.Fatalf("%s was closable", id)
		}
	}
}

func TestCloseProtectedCallbackAndOtherProtectedStillClosable(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["CB"] = &target{id: "CB", typ: "page", url: "https://seven.sliccy.ai/os/callback.html", protected: true}
	g.targets["FRAME"] = &target{id: "FRAME", typ: "page", url: "https://seven.sliccy.ai/frame", protected: true}
	for _, id := range []string{"CB", "FRAME"} {
		g.navTargets[id] = &navTarget{target: id, ready: make(chan struct{}), done: true}
		close(g.navTargets[id].ready)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range []string{"CB", "FRAME"} {
		if g.closeTargetReasonLocked(id) != "" {
			t.Fatalf("%s was not closable: %q", id, g.closeTargetReasonLocked(id))
		}
	}
}

func TestCloseProtectedCommittedKeptWhileNavigatingAway(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["OS"] = &target{id: "OS", typ: "page", url: "https://seven.sliccy.ai/os/", protected: true}
	g.navTargets["OS"] = &navTarget{target: "OS", ready: make(chan struct{}), done: true, documentURL: "https://seven.sliccy.ai/os/", unsure: true, navigationURL: "https://ordinary.test/slow"}
	close(g.navTargets["OS"].ready)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("OS") == "" {
		t.Fatal("live /os/ tab navigating to an ordinary URL was closable")
	}
}

func TestCloseProtectedUnknownInFlightRefuses(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["P"] = &target{id: "P", typ: "page", url: "https://ordinary.test/page", protected: true}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true, documentURL: "https://ordinary.test/page", unsure: true, navigationURL: ""}
	close(g.navTargets["P"].ready)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("P") == "" {
		t.Fatal("ordinary tab with unknown pending navigation was closable")
	}
}

func TestCloseProtectedErrorPageClosableDespiteLoadingAndOrigin(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["ERR"] = &target{id: "ERR", typ: "page", url: "https://vault.sliccy.ai/missing", protected: true}
	g.navTargets["ERR"] = &navTarget{
		target: "ERR", ready: make(chan struct{}), done: true,
		documentURL: "chrome-error://chromewebdata/", errorURL: "https://vault.sliccy.ai/missing",
		securityOrigin: "https://seven.sliccy.ai", unsure: true, loading: map[string]int{"L": 1},
		errorRecorded: true, errorNavGen: 0,
	}
	close(g.navTargets["ERR"].ready)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("ERR") != "" {
		t.Fatal("recorded protected error page was not closable")
	}
}

func TestCloseProtectedErrorThenReloadToOSRefuses(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["ERR"] = &target{id: "ERR", typ: "page", url: "https://seven.sliccy.ai/missing", protected: true}
	g.navTargets["ERR"] = &navTarget{
		target: "ERR", ready: make(chan struct{}), done: true,
		documentURL: "chrome-error://chromewebdata/", errorURL: "https://seven.sliccy.ai/missing",
		errorRecorded: true, errorNavGen: 0, navGen: 0,
	}
	close(g.navTargets["ERR"].ready)
	g.navTargets["ERR"].navGen = 1
	g.navTargets["ERR"].unsure = true
	g.navTargets["ERR"].navigationURL = "https://seven.sliccy.ai/os/"
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("ERR") == "" {
		t.Fatal("error page reloading to /os/ was closable")
	}
}

func TestCloseProtectedStaleCloseProofFailsClosed(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["T"] = &target{id: "T", typ: "page", url: "https://ordinary.test/", protected: true}
	g.navTargets["T"] = &navTarget{target: "T", session: "S", ready: make(chan struct{}), done: true, navGen: 1, revision: 3, unsure: true}
	close(g.navTargets["T"].ready)
	raw := []byte(`{"frameTree":{"frame":{"id":"T","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":"https://seven.sliccy.ai/missing"}}}`)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.applyCloseProofLocked("T", message{Result: raw}, 0, 3) {
		t.Fatal("stale close proof generation was accepted")
	}
	g.navTargets["T"].navGen = 2
	if g.applyCloseProofLocked("T", message{Result: raw}, 1, 3) {
		t.Fatal("close proof from before a navigation start was accepted")
	}
	g.navTargets["T"].navGen = 1
	if !g.applyCloseProofLocked("T", message{Result: raw}, 1, 3) {
		t.Fatal("matching close proof was refused")
	}
	if !g.navTargets["T"].errorRecorded || g.navTargets["T"].errorNavGen != 1 {
		t.Fatalf("error record: recorded=%t gen=%d", g.navTargets["T"].errorRecorded, g.navTargets["T"].errorNavGen)
	}
	g.navTargets["T"].navGen = 2
	g.navTargets["T"].navigationURL = "https://seven.sliccy.ai/os/"
	g.navTargets["T"].unsure = true
	if g.closeTargetFinalLocked("T") == "" {
		t.Fatal("after error proof, reload to /os/ was closable")
	}
}

func TestCloseProtectedCommitDuringCloseProofFailsClosed(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["T"] = &target{id: "T", typ: "page", url: "https://seven.sliccy.ai/missing", protected: true}
	g.navTargets["T"] = &navTarget{
		target: "T", session: "S", ready: make(chan struct{}), done: true,
		documentURL: "chrome-error://chromewebdata/", unsure: true, navigationURL: "",
		navGen: 1, revision: 4,
	}
	close(g.navTargets["T"].ready)
	raw := []byte(`{"frameTree":{"frame":{"id":"T","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":"https://seven.sliccy.ai/missing"}}}`)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.navTargets["T"].documentURL = "https://seven.sliccy.ai/os/"
	g.navTargets["T"].navigationURL = ""
	g.navTargets["T"].unsure = false
	g.navTargets["T"].revision = 5
	if g.applyCloseProofLocked("T", message{Result: raw}, 1, 4) {
		t.Fatal("close proof answered before /os/ commit was accepted after the commit")
	}
	if g.navTargets["T"].documentURL != "https://seven.sliccy.ai/os/" {
		t.Fatalf("stale error snapshot overwrote committed /os/: %s", g.navTargets["T"].documentURL)
	}
	if g.closeTargetFinalLocked("T") == "" {
		t.Fatal("live /os/ tab was closable after a rejected stale error proof")
	}
}

func TestCloseProtectedBlankPopupFromOSWithoutNavSession(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["OS"] = &target{id: "OS", typ: "page", url: "https://seven.sliccy.ai/os/", protected: true}
	g.targets["BLANK"] = &target{id: "BLANK", typ: "page", url: "about:blank", opener: "OS", protected: true}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.markStreamOpenedLocked("BLANK")
	if !g.keptURLLatch["BLANK"] {
		t.Fatal("blank popup from /os/ was not latched at open")
	}
	if g.closeTargetReasonLocked("BLANK") == "" {
		t.Fatal("blank popup from /os/ without a nav session was closable")
	}
	delete(g.keptURLLatch, "BLANK")
	if g.closeTargetReasonLocked("BLANK") == "" {
		t.Fatal("blank popup from /os/ was closable without latch when opener is kept")
	}
}

func TestCloseProtectedBlankKeepsLatchedOriginAfterOpenerGone(t *testing.T) {
	g := closeProtectedGuard(t)
	g.targets["BLANK"] = &target{id: "BLANK", typ: "page", url: "about:blank", opener: "GONE", protected: true}
	g.navTargets["BLANK"] = &navTarget{target: "BLANK", ready: make(chan struct{}), done: true, documentURL: "about:blank", securityOrigin: "https://seven.sliccy.ai", keptOrigin: "https://seven.sliccy.ai"}
	close(g.navTargets["BLANK"].ready)
	g.keptURLLatch["BLANK"] = true
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closeTargetReasonLocked("BLANK") == "" {
		t.Fatal("inherited blank was closable after opener was gone")
	}
}

func TestCloseProtectedVaultPathsKept(t *testing.T) {
	g := closeProtectedGuard(t)
	for i, url := range []string{
		"https://vault.sliccy.ai/unlock",
		"https://vault.sliccy.ai/Landing",
		"https://vault.sliccy.ai//",
		"http://vault.sliccy.ai:8080/doc",
		"https://vault-x.sliccy.ai/preview",
	} {
		id := fmt.Sprintf("V%d", i)
		g.targets[id] = &target{id: id, typ: "page", url: url, protected: true}
		g.navTargets[id] = &navTarget{target: id, ready: make(chan struct{}), done: true}
		close(g.navTargets[id].ready)
		g.mu.Lock()
		refused := g.closeTargetReasonLocked(id) != ""
		g.mu.Unlock()
		if !refused {
			t.Fatalf("%s was closable", url)
		}
	}
}
