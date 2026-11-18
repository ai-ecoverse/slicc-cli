package cdp

import (
	"encoding/json"
	"strings"
	"testing"

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
	injectNamedWorld(g, "S", "http://localhost:7002", "MID", "slicc-ext-probe-forged-unclassified", "unclass-unique", 31)
	g.mu.Lock()
	held := g.contextHeldLocked("PAGE", 31)
	g.mu.Unlock()
	if !held {
		if locked, _ := pageLatch(g); !locked {
			t.Fatal("unclassified MID world was neither held nor latched")
		}
	}
	v := g.fromCommand(&command{id: 50, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.forward == nil {
		t.Fatalf("getFrameTree to classify MID not forwarded: reply=%s", v.reply)
	}
	g.fromBrowser(encode(map[string]any{"id": 50, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
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
