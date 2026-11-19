package cdp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func injectExtensionWorld(g *guard, session, origin, frame, unique string, id int64) {
	injectNamedWorld(g, session, origin, frame, "", unique, id)
}

func injectNamedWorld(g *guard, session, origin, frame, name, unique string, id int64) {
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": session,
		"params": map[string]any{"context": map[string]any{
			"id": id, "origin": origin, "name": name, "uniqueId": unique,
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": frame},
		}},
	})
	_ = g.fromBrowser(raw)
}

func nestTreeReply(id int64) []byte {
	return encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
		"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/nest", "securityOrigin": "http://localhost:7002"},
		"childFrames": []any{
			map[string]any{"frame": map[string]any{"id": "HOST", "parentId": "PAGE", "url": "about:blank", "securityOrigin": "http://localhost:7002"}, "childFrames": []any{
				map[string]any{"frame": map[string]any{"id": "INNER", "parentId": "HOST", "url": "http://localhost:7002/inner", "securityOrigin": "http://localhost:7002"}},
			}},
		},
	}}})
}

func ordinaryNestPage(t *testing.T, g *guard) {
	t.Helper()
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.mu.Unlock()
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.forward == nil {
		t.Fatalf("client getFrameTree not forwarded: reply=%s", v.reply)
	}
	g.fromBrowser(nestTreeReply(1))
}

func pageLatch(g *guard) (locked bool, pf string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	locked = g.framesProtectedLocked("PAGE")
	if w := g.worlds["PAGE"]; w != nil {
		for _, c := range w.contexts {
			if c.protectedFrame != "" {
				pf = c.protectedFrame
			}
		}
	}
	return locked, pf
}

func TestExtensionWorldOnAboutBlankHostDoesNotLockPage(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001", "chrome-extension://sevenextid"}), t.Logf)
	ordinaryNestPage(t, g)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "HOST", "foreign-on-host", 7)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "INNER", "foreign-on-inner", 8)
	injectExtensionWorld(g, "S", "chrome-extension://sevenextid", "HOST", "seven-on-host", 9)
	injectExtensionWorld(g, "S", "chrome-extension://sevenextid", "INNER", "seven-on-inner", 10)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("extension worlds on about:blank/same-origin children locked the page: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree on the ordinary page: forward=%v reply=%s", v.forward != nil, v.reply)
	}
	w := g.fromCommand(&command{id: 3, method: "Page.createIsolatedWorld", session: "S", params: map[string]any{"frameId": "INNER", "worldName": "probe"}}, 1, true)
	if w.reply != nil || w.forward == nil {
		t.Fatalf("Page.createIsolatedWorld on /inner: forward=%v reply=%s", w.forward != nil, w.reply)
	}
}

func TestExtensionWorldUnderProtectedParentStillRefuses(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/framed-seven", "attached": true}
	seven := map[string]any{"targetId": "SEVEN", "type": "iframe", "url": "http://127.0.0.1:7001/frame.html", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": seven}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.frames["PAGE"] = map[string]bool{"SEVEN": true, "CHILD": true}
	_, _ = g.noteFrameParentLocked("SEVEN", "PAGE")
	_, _ = g.noteFrameParentLocked("CHILD", "SEVEN")
	_ = g.noteChildFrameLocked("SEVEN", "http://127.0.0.1:7001/frame.html")
	_ = g.noteChildFrameLocked("CHILD", "about:blank")
	if t7 := g.targets["SEVEN"]; t7 != nil {
		t7.protected = true
	}
	g.mu.Unlock()
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "CHILD", "ext-under-seven", 11)
	g.mu.Lock()
	under := g.frameUnderProtectedLocked("CHILD")
	g.mu.Unlock()
	if !under {
		t.Fatal("child under a protected parent must stay classified by ancestry")
	}
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply == nil {
		t.Fatalf("Page.getFrameTree on a page with a protected frame must stay refused, got forward=%v", v.forward != nil)
	}
}

func TestExtensionOwnProbeWorldInOrdinaryChildDoesNotLatch(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	ordinaryNestPage(t, g)
	const ownName = "slicc-ext-probe-own-a1b2c3d4"
	g.mu.Lock()
	g.storeExtensionProbeWorldLocked("S", "HOST", "http://localhost:7002/nest", 1, ownName, 15)
	g.mu.Unlock()
	injectNamedWorld(g, "S", "http://localhost:7002", "HOST", ownName, "own-probe-unique", 15)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("guard's own createIsolatedWorld world in an ordinary child latched the page: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree after own probe world: forward=%v reply=%s", v.forward != nil, v.reply)
	}
	w := g.fromCommand(&command{id: 3, method: "Page.createIsolatedWorld", session: "S", params: map[string]any{"frameId": "INNER", "worldName": "client-world"}}, 1, true)
	if w.reply != nil || w.forward == nil {
		t.Fatalf("Page.createIsolatedWorld on ordinary child after own probe: forward=%v reply=%s", w.forward != nil, w.reply)
	}
}

func TestExtensionProbeNamedForeignWorldOnOrdinaryDoesNotLatch(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	ordinaryNestPage(t, g)
	injectNamedWorld(g, "S", "http://localhost:7002", "INNER", "slicc-ext-probe-forged-by-contentscript", "forged-unique", 21)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("a content-script world named like slicc-ext-probe-* latched an ordinary frame: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree after forged probe name on ordinary: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}

func TestExtensionProbeNamedForeignWorldUnderProtectedStillRefuses(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/framed-seven", "attached": true}
	seven := map[string]any{"targetId": "SEVEN", "type": "iframe", "url": "http://127.0.0.1:7001/frame.html", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": seven}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.frames["PAGE"] = map[string]bool{"SEVEN": true, "CHILD": true}
	_, _ = g.noteFrameParentLocked("SEVEN", "PAGE")
	_, _ = g.noteFrameParentLocked("CHILD", "SEVEN")
	_ = g.noteChildFrameLocked("SEVEN", "http://127.0.0.1:7001/frame.html")
	_ = g.noteChildFrameLocked("CHILD", "about:blank")
	if t7 := g.targets["SEVEN"]; t7 != nil {
		t7.protected = true
	}
	g.mu.Unlock()
	injectNamedWorld(g, "S", "http://localhost:7002", "CHILD", "slicc-ext-probe-forged-by-contentscript", "forged-under-seven", 22)
	if locked, _ := pageLatch(g); !locked {
		t.Fatal("a forged probe-named world under a protected frame must still lock the page")
	}
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply == nil {
		t.Fatalf("Page.getFrameTree under protected+forged probe name must stay refused, got forward=%v", v.forward != nil)
	}
}

func TestExtensionUnclassifiedHideReleasesWhenFrameOrdinaryThenLatchesWhenProtected(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.mu.Unlock()
	created, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 31, "origin": "http://localhost:7002", "name": "slicc-ext-probe-forged-unclassified", "uniqueId": "unclass-unique",
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "MID"},
		}},
	})
	createdVerdict := g.fromBrowser(created)
	if locked, _ := pageLatch(g); !locked {
		t.Fatal("unclassified MID world must latch at once")
	}
	id := privateTreeID(t, createdVerdict.send)
	g.fromBrowser(encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
		"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/nest", "securityOrigin": "http://localhost:7002"},
		"childFrames": []any{
			map[string]any{"frame": map[string]any{"id": "MID", "parentId": "PAGE", "url": "http://localhost:7002/inner", "securityOrigin": "http://localhost:7002"}},
		},
	}}}))
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("latch stayed after MID classified ordinary: protectedFrame=%q", pf)
	}
	ok := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if ok.reply != nil || ok.forward == nil {
		t.Fatalf("Page.getFrameTree after ordinary classify: forward=%v reply=%s", ok.forward != nil, ok.reply)
	}
	g.mu.Lock()
	_ = g.noteChildFrameLocked("MID", "http://127.0.0.1:7001/embed")
	g.queueContextSettleLocked()
	_ = g.takeContextSettlePageLocked()
	_ = g.takeContextSettleSendLocked()
	g.mu.Unlock()
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "S", "params": map[string]any{"frame": map[string]any{"id": "MID", "parentId": "PAGE", "url": "http://127.0.0.1:7001/embed", "securityOrigin": "http://127.0.0.1:7001"}}}))
	if locked, _ := pageLatch(g); !locked {
		t.Fatal("frame classified protected later must latch again")
	}
	refused := g.fromCommand(&command{id: 3, method: "Page.getFrameTree", session: "S"}, 1, true)
	if refused.reply == nil || !strings.Contains(string(refused.reply), "refused") {
		t.Fatalf("Page.getFrameTree after late protect must refuse, got forward=%v reply=%s", refused.forward != nil, refused.reply)
	}
}

func TestExtensionOwnProbeEventBeforeReplyDoesNotLatch(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001", "chrome-extension://sevenextid"}), t.Logf)
	ordinaryNestPage(t, g)
	const token = "slicc-ext-probe-pending-before-reply"
	g.mu.Lock()
	if g.extensionProbePending == nil {
		g.extensionProbePending = map[extensionWorldKey]*extensionWorldPending{}
	}
	if g.extensionProbeWorlds == nil {
		g.extensionProbeWorlds = map[string]bool{}
	}
	g.extensionProbeWorlds[token] = true
	g.extensionProbePending[extensionWorldKey{"S", "HOST"}] = &extensionWorldPending{
		name: token, session: "S", frame: "HOST", document: "doc", revision: 1,
	}
	_ = g.noteChildFrameLocked("HOST", "chrome-extension://sevenextid/panel.html")
	g.mu.Unlock()
	injectNamedWorld(g, "S", "http://localhost:7002", "HOST", token, "event-before-reply-unique", 41)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("own probe executionContextCreated before createIsolatedWorld reply latched: protectedFrame=%q", pf)
	}
	g.mu.Lock()
	own := g.extensionProbeIDs[41] || g.extensionProbeUniques["event-before-reply-unique"]
	g.mu.Unlock()
	if !own {
		t.Fatal("pending probe match must bind context id/uniqueId from the event before the reply")
	}
	v := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree after event-before-reply probe: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}

func TestExtensionCopiedProbeTokenAfterRecordGoneIsNotOwn(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001", "chrome-extension://sevenextid"}), t.Logf)
	ordinaryNestPage(t, g)
	const token = "slicc-ext-probe-used-token-abc"
	g.mu.Lock()
	g.storeExtensionProbeWorldLocked("S", "HOST", "http://localhost:7002/nest", 1, token, 50)
	old := g.extensionProbeCache[extensionWorldKey{"S", "HOST"}]
	g.forgetExtensionProbeWorldLocked(old)
	if g.extensionProbePending != nil {
		delete(g.extensionProbePending, extensionWorldKey{"S", "HOST"})
	}
	if g.extensionProbeWorlds == nil {
		g.extensionProbeWorlds = map[string]bool{}
	}
	g.extensionProbeWorlds[token] = true
	_ = g.noteChildFrameLocked("HOST", "chrome-extension://sevenextid/panel.html")
	g.mu.Unlock()
	injectNamedWorld(g, "S", "http://localhost:7002", "HOST", token, "copied-token-unique", 51)
	g.mu.Lock()
	own := g.extensionProbeIDs[51] || g.extensionProbeUniques["copied-token-unique"]
	locked := g.framesProtectedLocked("PAGE")
	g.mu.Unlock()
	if own {
		t.Fatal("a content-script world copying a used probe token must not match a gone pending/recorded probe")
	}
	if locked {
		t.Fatalf("a forged probe-named world in a chrome-extension frame must not latch the page after the record is gone")
	}
}

func TestExtensionPendingProbeRecordExpires(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "20ms")
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	ordinaryNestPage(t, g)
	const token = "slicc-ext-probe-expire-me"
	g.mu.Lock()
	if g.extensionProbePending == nil {
		g.extensionProbePending = map[extensionWorldKey]*extensionWorldPending{}
	}
	g.extensionProbePending[extensionWorldKey{"S", "HOST"}] = &extensionWorldPending{
		name: token, session: "S", frame: "HOST", document: "doc", revision: 1, at: time.Now().Add(-time.Second),
	}
	if g.extensionProbeWorlds == nil {
		g.extensionProbeWorlds = map[string]bool{}
	}
	g.extensionProbeWorlds[token] = true
	var ctx execContext
	ctx.Name = token
	ctx.AuxData.FrameID = "HOST"
	matched := g.pendingExtensionProbeMatchLocked("S", ctx)
	_, pendingLeft := g.extensionProbePending[extensionWorldKey{"S", "HOST"}]
	g.mu.Unlock()
	if matched != nil || pendingLeft {
		t.Fatalf("expired pending probe record must be removed: matched=%v pending=%v", matched != nil, pendingLeft)
	}
}

func TestExtensionFrameURLDoesNotLatchPage(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001", "chrome-extension://sevenextid"}), t.Logf)
	ordinaryNestPage(t, g)
	g.mu.Lock()
	_ = g.noteChildFrameLocked("HOST", "chrome-extension://sevenextid/panel.html")
	g.mu.Unlock()
	injectNamedWorld(g, "S", "http://localhost:7002", "HOST", "slicc-ext-probe-forged-ext-frame", "ext-frame-unique", 61)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("world in a chrome-extension:// frame latched the page: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree with extension-frame child: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}

func privateTreeID(t *testing.T, send [][]byte) int64 {
	t.Helper()
	for _, raw := range send {
		var c struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Method == "Page.getFrameTree" {
			return c.ID
		}
	}
	t.Fatalf("no private Page.getFrameTree in send=%q", send)
	return 0
}

func TestExtensionForeignWorldOnUnclassifiedReleasesAfterPrivateTree(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.mu.Unlock()
	created, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 71, "origin": "http://localhost:7002", "name": "slicc-ext-probe-forged-foreign-unclass", "uniqueId": "foreign-unclass",
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "MID"},
		}},
	})
	createdVerdict := g.fromBrowser(created)
	if locked, pf := pageLatch(g); !locked {
		t.Fatalf("foreign world on an unclassified ordinary child must latch at once: protectedFrame=%q held=%v send=%q", pf, func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.contextHeldLocked("PAGE", 71)
		}(), createdVerdict.send)
	}
	id := privateTreeID(t, createdVerdict.send)
	clientMsgs := append([][]byte{}, createdVerdict.page...)
	if len(createdVerdict.forward) > 0 {
		clientMsgs = append(clientMsgs, createdVerdict.forward)
	}
	for _, raw := range clientMsgs {
		var m struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Result any    `json:"result"`
		}
		if json.Unmarshal(raw, &m) == nil && m.ID != nil && *m.ID == id {
			t.Fatalf("private Page.getFrameTree must not reach the client: %s", raw)
		}
	}
	g.fromBrowser(encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
		"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/nest", "securityOrigin": "http://localhost:7002"},
		"childFrames": []any{
			map[string]any{"frame": map[string]any{"id": "MID", "parentId": "PAGE", "url": "http://localhost:7002/inner", "securityOrigin": "http://localhost:7002"}},
		},
	}}}))
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("private tree classifying MID ordinary must release the latch: protectedFrame=%q", pf)
	}
	ok := g.fromCommand(&command{id: 2, method: "Page.getFrameTree", session: "S"}, 1, true)
	if ok.reply != nil || ok.forward == nil {
		t.Fatalf("Page.getFrameTree after private-tree release: forward=%v reply=%s", ok.forward != nil, ok.reply)
	}
}

func TestExtensionProtectedBlankChildOfSevenNeverReleasedByPrivateTree(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/framed-seven", "attached": true}
	seven := map[string]any{"targetId": "SEVEN", "type": "iframe", "url": "http://127.0.0.1:7001/frame.html", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": seven}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.frames["PAGE"] = map[string]bool{"SEVEN": true, "CHILD": true}
	_, _ = g.noteFrameParentLocked("SEVEN", "PAGE")
	_, _ = g.noteFrameParentLocked("CHILD", "SEVEN")
	_ = g.noteChildFrameLocked("SEVEN", "http://127.0.0.1:7001/frame.html")
	_ = g.noteChildFrameLocked("CHILD", "about:blank")
	if t7 := g.targets["SEVEN"]; t7 != nil {
		t7.protected = true
	}
	g.mu.Unlock()
	created, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 81, "origin": "http://127.0.0.1:7001", "name": "", "uniqueId": "blank-under-seven",
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "CHILD"},
		}},
	})
	createdVerdict := g.fromBrowser(created)
	if locked, _ := pageLatch(g); !locked {
		t.Fatal("protected about:blank child of a seven page must latch")
	}
	g.mu.Lock()
	send := g.worldEpochLocked("PAGE", "S")
	g.mu.Unlock()
	if len(send) == 0 {
		send = createdVerdict.send
	}
	id := privateTreeID(t, send)
	tree := g.fromBrowser(encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
		"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/framed-seven", "securityOrigin": "http://localhost:7002"},
		"childFrames": []any{
			map[string]any{"frame": map[string]any{"id": "SEVEN", "parentId": "PAGE", "url": "http://127.0.0.1:7001/frame.html", "securityOrigin": "http://127.0.0.1:7001"}, "childFrames": []any{
				map[string]any{"frame": map[string]any{"id": "CHILD", "parentId": "SEVEN", "url": "about:blank", "securityOrigin": "http://127.0.0.1:7001"}},
			}},
		},
	}}}))
	clientTree := append([][]byte{}, tree.page...)
	if len(tree.forward) > 0 {
		clientTree = append(clientTree, tree.forward)
	}
	for _, raw := range clientTree {
		var m struct {
			Method string `json:"method"`
			Result any    `json:"result"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Result != nil {
			t.Fatalf("private tree result must not reach the client: %s", raw)
		}
	}
	if locked, pf := pageLatch(g); !locked {
		t.Fatalf("private tree saying seven/unknown must not release a protected about:blank child: protectedFrame=%q", pf)
	}
	for _, ev := range []map[string]any{
		{"method": "DOM.attributeModified", "sessionId": "S", "params": map[string]any{"nodeId": 1, "name": "src", "value": "about:blank", "frameId": "CHILD"}},
		{"method": "Accessibility.loadComplete", "sessionId": "S", "params": map[string]any{"frameId": "CHILD", "root": map[string]any{"role": map[string]any{"value": "WebArea"}}}},
		{"method": "DOM.documentUpdated", "sessionId": "S", "params": map[string]any{"frameId": "CHILD"}},
	} {
		leak := g.fromBrowser(encode(ev))
		if len(leak.forward) > 0 {
			t.Fatalf("DOM/Accessibility event from protected about:blank child leaked to the client: method=%s forward=%s", ev["method"], leak.forward)
		}
	}
	srcdoc, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 82, "origin": "http://127.0.0.1:7001", "name": "", "uniqueId": "srcdoc-under-seven",
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "SRCDOC"},
		}},
	})
	g.mu.Lock()
	_, _ = g.noteFrameParentLocked("SRCDOC", "SEVEN")
	_ = g.noteChildFrameLocked("SRCDOC", "about:srcdoc")
	g.mu.Unlock()
	_ = g.fromBrowser(srcdoc)
	g.mu.Lock()
	send = g.worldEpochLocked("PAGE", "S")
	g.mu.Unlock()
	if len(send) > 0 {
		sid := privateTreeID(t, send)
		g.fromBrowser(encode(map[string]any{"id": sid, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
			"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/framed-seven", "securityOrigin": "http://localhost:7002"},
			"childFrames": []any{
				map[string]any{"frame": map[string]any{"id": "SEVEN", "parentId": "PAGE", "url": "http://127.0.0.1:7001/frame.html", "securityOrigin": "http://127.0.0.1:7001"}, "childFrames": []any{
					map[string]any{"frame": map[string]any{"id": "SRCDOC", "parentId": "SEVEN", "url": "about:srcdoc", "securityOrigin": "http://127.0.0.1:7001"}},
				}},
			},
		}}}))
	}
	if locked, pf := pageLatch(g); !locked {
		t.Fatalf("protected about:srcdoc child of seven must stay latched after private tree: protectedFrame=%q", pf)
	}
}
