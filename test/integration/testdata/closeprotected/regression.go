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
