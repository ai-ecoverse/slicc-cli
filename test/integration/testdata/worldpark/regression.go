package cdp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func worldGuard(t *testing.T) *guard {
	t.Helper()
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "P", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	for _, ev := range []map[string]any{
		{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}},
		{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}},
	} {
		g.fromBrowser(encode(ev))
	}
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["P"] = &navTarget{target: "P", session: "S", ready: ready, done: true}
	g.mu.Unlock()
	return g
}

func worldCmd(id int64, frame string) *command {
	return &command{id: id, method: "Page.createIsolatedWorld", session: "S", params: map[string]any{"frameId": frame, "worldName": "w"}}
}

func treeReply(id int64, tree any) []byte {
	return encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"frameTree": tree}})
}

func fr(id, parent, url, origin string, kids ...any) map[string]any {
	f := map[string]any{"id": id, "url": url, "securityOrigin": origin}
	if parent != "" {
		f["parentId"] = parent
	}
	return map[string]any{"frame": f, "childFrames": kids}
}

var nestTree = fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
	fr("A", "P", "about:blank", "http://localhost:7002", fr("B", "A", "http://localhost:7002/inner", "http://localhost:7002")),
	fr("X", "P", "http://127.0.0.1:7001/x", "http://127.0.0.1:7001", fr("Y", "X", "about:blank", "http://127.0.0.1:7001")),
	map[string]any{"frame": map[string]any{"id": "E", "parentId": "P", "url": "chrome-error://chromewebdata/", "securityOrigin": "://", "unreachableUrl": "http://127.0.0.1:7001/blocked"}})

func probeID(t *testing.T, send [][]byte) int64 {
	t.Helper()
	for _, m := range send {
		var c struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(m, &c) == nil && c.Method == "Page.getFrameTree" {
			return c.ID
		}
	}
	t.Fatalf("no private Page.getFrameTree in %q", send)
	return 0
}

func forwarded(v pageVerdict) bool { return v.forward != nil && v.reply == nil && v.wait == nil }

func refusedWith(v pageVerdict) string {
	var m struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(v.reply, &m)
	return m.Error.Message
}

func reattachWorldPage(t *testing.T, g *guard) {
	t.Helper()
	page := map[string]any{"targetId": "P", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["P"] = &navTarget{target: "P", session: "S", ready: ready, done: true}
	g.mu.Unlock()
}

func TestClientFrameTreeClassifiesPreexistingFrames(t *testing.T) {
	g := worldGuard(t)
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if !forwarded(v) {
		t.Fatal("client tree call not forwarded")
	}
	g.fromBrowser(treeReply(1, nestTree))
	for i, f := range []string{"A", "B"} {
		if v := g.fromCommand(worldCmd(int64(20+i), f), int64(20+i), true); !forwarded(v) {
			t.Fatalf("world for ordinary pre-existing frame %s not forwarded at once: wait=%v reply=%s", f, v.wait != nil, v.reply)
		}
	}
	for i, f := range []string{"X", "Y", "E"} {
		v := g.fromCommand(worldCmd(int64(30+i), f), int64(30+i), true)
		if v.wait != nil || !strings.HasPrefix(refusedWith(v), "slicc: refused: ") {
			t.Fatalf("world for %s under a protected frame: wait=%v reply=%s", f, v.wait != nil, v.reply)
		}
	}
}

func TestPrivateTreeProbeReleasesParkedWorld(t *testing.T) {
	g := worldGuard(t)
	v := g.fromCommand(worldCmd(5, "B"), 5, true)
	if v.wait == nil {
		t.Fatal("world for an unknown frame was not parked")
	}
	id := probeID(t, v.send)
	g.fromBrowser(treeReply(id, nestTree))
	select {
	case <-v.wait:
	default:
		t.Fatal("the private tree did not release the wait")
	}
	if v := g.fromCommand(worldCmd(5, "B"), 5, true); !forwarded(v) {
		t.Fatalf("world not forwarded after classification: %s", v.reply)
	}
}

func TestUnclassifiedFramesShareOneDeadline(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "150ms")
	g := worldGuard(t)
	a := g.fromCommand(worldCmd(7, "U1"), 7, true)
	if a.wait == nil {
		t.Fatal("unknown frame not parked")
	}
	g.fromBrowser(treeReply(probeID(t, a.send), nestTree))
	start := time.Now()
	select {
	case <-a.wait:
	case <-time.After(2 * time.Second):
		t.Fatal("deadline never closed the wait")
	}
	if v := g.fromCommand(worldCmd(7, "U1"), 7, true); !strings.HasPrefix(refusedWith(v), "slicc: refused: ") {
		t.Fatalf("unclassified at the deadline must be refused: %s", v.reply)
	}
	b := g.fromCommand(worldCmd(8, "U2"), 8, true)
	if b.wait != nil || !strings.HasPrefix(refusedWith(b), "slicc: refused: ") {
		t.Fatalf("a second unclassified frame of the page started a new wait: wait=%v reply=%s", b.wait != nil, b.reply)
	}
	if time.Since(start) > time.Second {
		t.Fatal("too slow")
	}
}

func TestInconsistentTreeLeavesFrameUnclassified(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "150ms")
	g := worldGuard(t)
	g.mu.Lock()
	_, _ = g.noteFrameParentLocked("B", "Z")
	g.mu.Unlock()
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, nestTree))
	v := g.fromCommand(worldCmd(9, "B"), 9, true)
	if forwarded(v) {
		t.Fatal("a frame with a disputed parent was classified")
	}
	wrong := fr("Q", "", "http://localhost:7002/", "http://localhost:7002", fr("C", "Q", "http://localhost:7002/c", "http://localhost:7002"))
	g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 2, true)
	g.fromBrowser(treeReply(2, wrong))
	if v := g.fromCommand(worldCmd(10, "C"), 10, true); forwarded(v) {
		t.Fatal("a tree rooted at another target classified its frames")
	}
}

func TestAncestryDisputedClearedWhenPageDestroyed(t *testing.T) {
	g := worldGuard(t)
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, bad))
	if forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("a disputed frame was classified")
	}
	g.mu.Lock()
	if !g.ancestryDisputed["F"] {
		g.mu.Unlock()
		t.Fatal("tree mismatch did not mark the frame disputed")
	}
	g.mu.Unlock()
	g.fromBrowser(encode(map[string]any{"method": "Target.targetDestroyed", "params": map[string]any{"targetId": "P"}}))
	reattachWorldPage(t, g)
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 3, method: "Page.getFrameTree", session: "S"}, 3, true)
	g.fromBrowser(treeReply(3, good))
	v := g.fromCommand(worldCmd(4, "F"), 4, true)
	if !forwarded(v) {
		g.mu.Lock()
		disputed := g.ancestryDisputed["F"]
		g.mu.Unlock()
		t.Fatalf("a fresh frame reusing a destroyed disputed id must classify: wait=%v reply=%s disputed=%v", v.wait != nil, v.reply, disputed)
	}
}

func TestAncestryDisputedClearedWhenFrameNavigated(t *testing.T) {
	g := worldGuard(t)
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, bad))
	if forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("a disputed frame was classified")
	}
	g.mu.Lock()
	if !g.ancestryDisputed["F"] {
		g.mu.Unlock()
		t.Fatal("tree mismatch did not mark the frame disputed")
	}
	g.mu.Unlock()
	g.fromBrowser(encode(map[string]any{
		"method":    "Page.frameNavigated",
		"sessionId": "S",
		"params": map[string]any{
			"frame": map[string]any{
				"id": "F", "parentId": "P", "loaderId": "L-F-2",
				"url": "http://localhost:7002/inner2", "securityOrigin": "http://localhost:7002",
			},
		},
	}))
	g.mu.Lock()
	still := g.ancestryDisputed["F"]
	g.mu.Unlock()
	if still {
		t.Fatal("a dispute recorded for a frame must not survive its navigation to a new document")
	}
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner2", "http://localhost:7002"))
	g.fromCommand(&command{id: 3, method: "Page.getFrameTree", session: "S"}, 3, true)
	g.fromBrowser(treeReply(3, good))
	v := g.fromCommand(worldCmd(4, "F"), 4, true)
	if !forwarded(v) {
		g.mu.Lock()
		disputed := g.ancestryDisputed["F"]
		g.mu.Unlock()
		t.Fatalf("after navigation a formerly disputed frame must classify: wait=%v reply=%s disputed=%v", v.wait != nil, v.reply, disputed)
	}
}

func fillAncestryDisputedCap(t *testing.T, g *guard, page string, session string, startID int64) {
	t.Helper()
	for round := 0; round < 3; round++ {
		kids := make([]any, 0, 2000)
		for i := 0; i < 2000; i++ {
			kids = append(kids, fr("R"+itoa(int64(round))+"F"+itoa(int64(i)), "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
		}
		tree := fr(page, "", "http://localhost:7002/nest", "http://localhost:7002", kids...)
		id := startID + int64(round)
		g.fromCommand(&command{id: id, method: "Page.getFrameTree", session: session}, id, true)
		g.fromBrowser(encode(map[string]any{"id": id, "sessionId": session, "result": map[string]any{"frameTree": tree}}))
	}
}

func TestAncestryDisputedCapFailsClosed(t *testing.T) {
	g := worldGuard(t)
	const wantCap = 4096
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, good))
	if !forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("classified frame F must forward before the map is full")
	}
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	g.mu.Lock()
	n := len(g.ancestryDisputed)
	g.mu.Unlock()
	if n > wantCap {
		t.Fatalf("ancestryDisputed grew to %d, over the bound of %d", n, wantCap)
	}
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 20, method: "Page.getFrameTree", session: "S"}, 20, true)
	g.fromBrowser(treeReply(20, bad))
	v := g.fromCommand(worldCmd(21, "F"), 21, true)
	g.mu.Lock()
	disputed := g.ancestryDisputed["F"]
	n = len(g.ancestryDisputed)
	g.mu.Unlock()
	if forwarded(v) {
		t.Fatalf("after the map is full a mismatched formerly-classified frame must not forward: wait=%v reply=%s disputed=%v map=%d", v.wait != nil, v.reply, disputed, n)
	}
}

func attachWorldPage(t *testing.T, g *guard, id, session, url string) {
	t.Helper()
	page := map[string]any{"targetId": id, "type": "page", "url": url, "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets[id] = &navTarget{target: id, session: session, ready: ready, done: true}
	g.mu.Unlock()
}

func TestAncestryDisputedOverflowClearedWhenPageNavigated(t *testing.T) {
	g := worldGuard(t)
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, good))
	if !forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("classified frame F must forward before overflow")
	}
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 20, method: "Page.getFrameTree", session: "S"}, 20, true)
	g.fromBrowser(treeReply(20, bad))
	if forwarded(g.fromCommand(worldCmd(21, "F"), 21, true)) {
		t.Fatal("overflow must refuse the mismatched frame before navigation clears it")
	}
	g.fromBrowser(encode(map[string]any{
		"method": "Page.frameNavigated", "sessionId": "S",
		"params": map[string]any{"frame": map[string]any{
			"id": "P", "loaderId": "L-P-2", "url": "http://localhost:7002/nest2", "securityOrigin": "http://localhost:7002",
		}},
	}))
	g.fromCommand(&command{id: 30, method: "Page.getFrameTree", session: "S"}, 30, true)
	g.fromBrowser(treeReply(30, good))
	if !forwarded(g.fromCommand(worldCmd(31, "F"), 31, true)) {
		t.Fatal("after navigation clears overflow a correctly parented frame must classify")
	}
}

func TestAncestryDisputedOverflowClearedWhenPageDestroyed(t *testing.T) {
	g := worldGuard(t)
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, good))
	if !forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("classified frame F must forward before overflow")
	}
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 20, method: "Page.getFrameTree", session: "S"}, 20, true)
	g.fromBrowser(treeReply(20, bad))
	if forwarded(g.fromCommand(worldCmd(21, "F"), 21, true)) {
		t.Fatal("overflow must refuse the mismatched frame before destroy clears it")
	}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetDestroyed", "params": map[string]any{"targetId": "P"}}))
	reattachWorldPage(t, g)
	g.fromCommand(&command{id: 30, method: "Page.getFrameTree", session: "S"}, 30, true)
	g.fromBrowser(treeReply(30, good))
	if !forwarded(g.fromCommand(worldCmd(31, "F"), 31, true)) {
		t.Fatal("after destroy clears overflow a correctly parented frame must classify")
	}
}

func TestAncestryDisputedOverflowLeavesOtherPagesUntouched(t *testing.T) {
	g := worldGuard(t)
	attachWorldPage(t, g, "Q", "SQ", "http://localhost:7002/other")
	goodP := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, goodP))
	if !forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("classified frame F on P must forward before overflow")
	}
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	badP := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 20, method: "Page.getFrameTree", session: "S"}, 20, true)
	g.fromBrowser(treeReply(20, badP))
	if forwarded(g.fromCommand(worldCmd(21, "F"), 21, true)) {
		t.Fatal("overflow on P must refuse F")
	}
	goodQ := fr("Q", "", "http://localhost:7002/other", "http://localhost:7002",
		fr("G", "Q", "http://localhost:7002/g", "http://localhost:7002"))
	g.fromCommand(&command{id: 30, method: "Page.getFrameTree", session: "SQ"}, 30, true)
	g.fromBrowser(encode(map[string]any{"id": 30, "sessionId": "SQ", "result": map[string]any{"frameTree": goodQ}}))
	vq := g.fromCommand(&command{id: 31, method: "Page.createIsolatedWorld", session: "SQ", params: map[string]any{"frameId": "G", "worldName": "w"}}, 31, true)
	if !forwarded(vq) {
		t.Fatalf("ordinary page with fewer disputes must stay untouched: wait=%v reply=%s", vq.wait != nil, vq.reply)
	}
}

func TestAncestryDisputedOverflowHoldsContextAndRefusesEvaluate(t *testing.T) {
	g := worldGuard(t)
	good := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "P", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	g.fromBrowser(treeReply(1, good))
	if !forwarded(g.fromCommand(worldCmd(2, "F"), 2, true)) {
		t.Fatal("classified frame F must forward before overflow")
	}
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	bad := fr("P", "", "http://localhost:7002/nest", "http://localhost:7002",
		fr("F", "WRONG", "http://localhost:7002/inner", "http://localhost:7002"))
	g.fromCommand(&command{id: 20, method: "Page.getFrameTree", session: "S"}, 20, true)
	g.fromBrowser(treeReply(20, bad))
	ctx := encode(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 7, "origin": "http://localhost:7002", "uniqueId": "ctx-F-7",
			"auxData": map[string]any{"isDefault": true, "frameId": "F"},
		}},
	})
	v := g.fromBrowser(ctx)
	g.mu.Lock()
	held := g.contextHeldLocked("P", 7)
	g.mu.Unlock()
	if v.forward != nil || !held {
		t.Fatalf("overflow must hold the context event: forward=%v held=%v", v.forward != nil, held)
	}
	ev := g.fromCommand(&command{id: 21, method: "Runtime.evaluate", session: "S", params: map[string]any{"contextId": json.Number("7"), "expression": "1"}}, 21, true)
	if forwarded(ev) || !strings.HasPrefix(refusedWith(ev), "slicc: refused: ") {
		t.Fatalf("evaluate in a held overflow context must be refused: wait=%v reply=%s", ev.wait != nil, ev.reply)
	}
}

func TestAncestryDisputedAttachOverflowFailsClosed(t *testing.T) {
	g := worldGuard(t)
	fillAncestryDisputedCap(t, g, "P", "S", 10)
	g.mu.Lock()
	g.targets["IF"] = &target{id: "IF", typ: "iframe", url: "http://127.0.0.1:7001/x"}
	g.frameParents["IF"] = "OTHER"
	g.attachParent = "S"
	_ = g.noteIframeAttachParentLocked(targetInfo{TargetID: "IF", Type: "iframe", URL: "http://127.0.0.1:7001/x", ParentFrameID: "MISSING"})
	reason := g.iframeProtectedAncestorReasonLocked(g.targets["IF"])
	disputed := g.ancestryDisputed["IF"]
	g.mu.Unlock()
	if reason == "" && !disputed {
		t.Fatal("iframe attach dispute at the ancestryDisputed cap must fail closed")
	}
}

func TestClassifiedDuringEpochRefusalHoldForwards(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "100ms")
	g := worldGuard(t)
	a := g.fromCommand(worldCmd(1, "U"), 1, true)
	if a.wait == nil {
		t.Fatal("unknown frame was not parked")
	}
	g.fromBrowser(treeReply(probeID(t, a.send), nestTree))
	select {
	case <-a.wait:
	case <-time.After(2 * time.Second):
		t.Fatal("deadline never closed the wait")
	}
	g.mu.Lock()
	e := g.worldEpochs["P"]
	if e == nil || !e.expired || !time.Now().Before(e.until) {
		g.mu.Unlock()
		t.Fatal("expected the page's refusal hold window to still be open")
	}
	_, _ = g.noteFrameParentLocked("U", "P")
	_ = g.noteChildFrameLocked("U", "http://localhost:7002/inner")
	g.mu.Unlock()
	v := g.fromCommand(worldCmd(2, "U"), 2, true)
	if !forwarded(v) {
		t.Fatalf("a frame classified during the epoch refusal hold must not be refused: wait=%v reply=%s", v.wait != nil, v.reply)
	}
}

func TestIsolatedWorldWaitIsTheOnlyWaitVerdictWithSend(t *testing.T) {
	g := worldGuard(t)
	v := g.fromCommand(worldCmd(1, "U"), 1, true)
	if v.wait == nil || len(v.send) == 0 {
		t.Fatalf("isolated-world wait must carry the private tree probe: wait=%v send=%d", v.wait != nil, len(v.send))
	}
	g.mu.Lock()
	gate := g.framesGateLocked("S")
	g.mu.Unlock()
	if gate != nil {
		t.Fatal("framesGate was unexpectedly open on a settled page")
	}
}
