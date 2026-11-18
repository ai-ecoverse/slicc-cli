package cdp

import (
	"encoding/json"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func TestExtensionWorldOnAboutBlankHostDoesNotLockPage(t *testing.T) {
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": "http://localhost:7002/nest", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true}
	g.frames["PAGE"] = map[string]bool{"HOST": true}
	_, _ = g.noteFrameParentLocked("HOST", "PAGE")
	_ = g.noteChildFrameLocked("HOST", "about:blank")
	g.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 7, "origin": "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "name": "", "uniqueId": "ext-on-host",
			"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": "HOST"},
		}},
	})
	_ = g.fromBrowser(raw)
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "S", "params": map[string]any{"frame": map[string]any{"id": "HOST", "parentId": "PAGE", "url": "about:blank", "securityOrigin": "http://localhost:7002"}}}))
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
		t.Fatalf("extension world on about:blank host locked the page: protectedFrame=%q", pf)
	}
	v := g.fromCommand(&command{id: 1, method: "Page.getFrameTree", session: "S"}, 1, true)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Page.getFrameTree on the ordinary page: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}
