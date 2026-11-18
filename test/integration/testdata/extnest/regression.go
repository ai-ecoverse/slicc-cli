package cdp

import (
	"encoding/json"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func injectExtensionWorld(g *guard, session, origin, frame, unique string, id int64) {
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": session,
		"params": map[string]any{"context": map[string]any{
			"id": id, "origin": origin, "name": "", "uniqueId": unique,
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": frame},
		}},
	})
	_ = g.fromBrowser(raw)
}

func TestExtensionWorldOnAboutBlankHostDoesNotLockPage(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001", "chrome-extension://sevenextid"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.frames["PAGE"] = map[string]bool{"HOST": true, "INNER": true}
	_, _ = g.noteFrameParentLocked("HOST", "PAGE")
	_, _ = g.noteFrameParentLocked("INNER", "HOST")
	_ = g.noteChildFrameLocked("HOST", "about:blank")
	_ = g.noteChildFrameLocked("INNER", "http://localhost:7002/inner")
	g.mu.Unlock()
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "HOST", "foreign-on-host", 7)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "INNER", "foreign-on-inner", 8)
	injectExtensionWorld(g, "S", "chrome-extension://sevenextid", "HOST", "seven-on-host", 9)
	injectExtensionWorld(g, "S", "chrome-extension://sevenextid", "INNER", "seven-on-inner", 10)
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "S", "params": map[string]any{"frame": map[string]any{"id": "HOST", "parentId": "PAGE", "url": "about:blank", "securityOrigin": "http://localhost:7002"}}}))
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "S", "params": map[string]any{"frame": map[string]any{"id": "INNER", "parentId": "HOST", "url": "http://localhost:7002/inner", "securityOrigin": "http://localhost:7002"}}}))
	g.mu.Lock()
	locked := g.framesProtectedLocked("PAGE")
	var pf string
	if w := g.worlds["PAGE"]; w != nil {
		for _, c := range w.contexts {
			if c.protectedFrame != "" {
				pf = c.protectedFrame
			}
		}
	}
	g.mu.Unlock()
	if locked {
		t.Fatalf("extension worlds on about:blank/same-origin children locked the page: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree on the ordinary page: forward=%v reply=%s", v.forward != nil, v.reply)
	}
	w := g.fromCommand(&command{id: 2, method: "Page.createIsolatedWorld", session: "S", params: map[string]any{"frameId": "INNER", "worldName": "probe"}}, 1, true)
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
