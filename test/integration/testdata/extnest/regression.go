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

const nestOrdinaryURL = "http://localhost:7002/nest"

func nestArmRelay(g *guard) {
	g.relaySession = "RELAY"
}

func nestSettle(g *guard, v browserVerdict, url string) browserVerdict {
	out := v
	for _, raw := range v.send {
		var call struct {
			ID        int64  `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &call) != nil || call.Method != "Target.getTargetInfo" || call.SessionID != "RELAY" {
			continue
		}
		info, _ := json.Marshal(map[string]any{
			"id": call.ID, "sessionId": "RELAY",
			"result": map[string]any{"targetInfo": map[string]any{"targetId": "PAGE", "type": "page", "url": url}},
		})
		settled := nestSettle(g, g.fromBrowser(info), url)
		if settled.forward != nil {
			out.forward = settled.forward
		}
		out.page = append(out.page, settled.page...)
		out.send = append(out.send, settled.send...)
	}
	if out.forward == nil && len(out.page) == 0 {
		g.mu.Lock()
		page := g.takePendingPageLocked()
		g.mu.Unlock()
		out.page = append(out.page, page...)
	}
	return out
}

func ordinaryNestPage(t *testing.T, g *guard) {
	t.Helper()
	nestArmRelay(g)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": nestOrdinaryURL, "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", ready: ready, done: true, fetchArmed: true, documentURL: nestOrdinaryURL}
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

const sevenOrigin = "http://127.0.0.1:7001"

var nestCalls int64 = 1000

func nestCommand(g *guard, session, method string, params map[string]any) pageVerdict {
	nestCalls++
	return g.fromCommand(&command{id: nestCalls, method: method, session: session, params: params}, nestCalls, true)
}

func guardedNestPage(t *testing.T, g *guard, done bool) *navTarget {
	t.Helper()
	nestArmRelay(g)
	page := map[string]any{"targetId": "PAGE", "type": "page", "url": nestOrdinaryURL, "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	defer g.mu.Unlock()
	ready := make(chan struct{})
	if done {
		close(ready)
	}
	nt := &navTarget{target: "PAGE", session: "N", ready: ready, done: done, fetchArmed: true, documentURL: nestOrdinaryURL}
	g.navTargets["PAGE"] = nt
	g.navSessions["N"] = nt
	return nt
}

func guardedMiddle(t *testing.T, g *guard) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.targets["MID"] = &target{id: "MID", typ: "iframe", url: "http://localhost:7003/mid", parentFrame: "PAGE", ctx: g.targets["PAGE"].ctx}
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: "MID", session: "NM", ready: ready, done: true, iframe: true}
	g.navTargets["MID"] = nt
	g.navSessions["NM"] = nt
}

func nestContext(g *guard, session string, id int64, origin, frame, name, unique string, isDefault bool) browserVerdict {
	typ := "isolated"
	if isDefault {
		typ = "default"
	}
	return g.fromBrowser(encode(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": session,
		"params": map[string]any{"context": map[string]any{
			"id": id, "origin": origin, "name": name, "uniqueId": unique,
			"auxData": map[string]any{"isDefault": isDefault, "type": typ, "frameId": frame},
		}},
	}))
}

func latchedOn(g *guard, target string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.framesProtectedLocked(target)
}

var frameContentReads = []struct {
	method string
	params map[string]any
}{
	{"Page.getFrameTree", nil},
	{"DOM.getDocument", map[string]any{"depth": -1, "pierce": true}},
	{"Accessibility.getFullAXTree", nil},
	{"DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []any{}}},
	{"Page.captureScreenshot", nil},
	{"Page.startScreencast", nil},
}

func frameContentRefused(t *testing.T, g *guard, why string) {
	t.Helper()
	for _, r := range frameContentReads {
		v := nestCommand(g, "S", r.method, r.params)
		if v.forward != nil || v.reply == nil || !strings.Contains(string(v.reply), "refused") {
			t.Errorf("%s: %s on the page must be refused: forward=%v reply=%s", why, r.method, v.forward != nil, v.reply)
		}
	}
}

func frameContentOpen(t *testing.T, g *guard, why string) {
	t.Helper()
	for _, r := range frameContentReads {
		if r.method == "Page.startScreencast" {
			continue
		}
		v := nestCommand(g, "S", r.method, r.params)
		if v.reply != nil || v.forward == nil {
			t.Errorf("%s: %s on the ordinary page: forward=%v reply=%s", why, r.method, v.forward != nil, v.reply)
		}
	}
}

func TestExtensionSevenInheritedChildLatchesThroughDefaultBesideExtensionWorld(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			for _, extFirst := range []bool{true, false} {
				name := kind + "/" + session
				if extFirst {
					name += "/extension-first"
				} else {
					name += "/default-first"
				}
				t.Run(name, func(t *testing.T) {
					g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
					guardedNestPage(t, g, true)
					frame := "CHILD-" + kind
					ext := func() {
						nestContext(g, session, 51, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "ext-"+kind, false)
					}
					if extFirst {
						ext()
					}
					nestContext(g, session, 50, sevenOrigin, frame, "", "def-"+kind, true)
					if !extFirst {
						ext()
					}
					if !latchedOn(g, "PAGE") {
						t.Fatalf("the default context of seven's unclassified %s child did not lock the page", kind)
					}
					frameContentRefused(t, g, "seven's "+kind+" child with an extension world")
				})
			}
		}
	}
}

func TestExtensionSevenInheritedChildInMiddleIframeLatchesThroughDefault(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		t.Run(kind, func(t *testing.T) {
			g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
			guardedNestPage(t, g, true)
			guardedMiddle(t, g)
			frame := "MID-CHILD-" + kind
			nestContext(g, "NM", 60, "http://localhost:7003", "MID", "", "mid-def", true)
			nestContext(g, "NM", 61, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "mid-ext-"+kind, false)
			if !latchedOn(g, "MID") {
				t.Fatalf("an extension world alone in the middle iframe's unclassified %s child did not lock it", kind)
			}
			nestContext(g, "NM", 62, sevenOrigin, frame, "", "mid-seven-"+kind, true)
			if !latchedOn(g, "MID") || !latchedOn(g, "PAGE") {
				t.Fatalf("seven's %s child inside the middle iframe did not lock it and the page: mid=%v page=%v", kind, latchedOn(g, "MID"), latchedOn(g, "PAGE"))
			}
			frameContentRefused(t, g, "seven's "+kind+" child in the middle iframe")
		})
	}
}

func TestExtensionWorldInClassifiedProtectedFrameLatches(t *testing.T) {
	for _, session := range []string{"N", "S"} {
		for _, origin := range []string{"chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "chrome-extension://sevenextid", sevenOrigin} {
			t.Run(session+"/"+origin, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin, "chrome-extension://sevenextid"}), t.Logf)
				guardedNestPage(t, g, true)
				g.mu.Lock()
				_, _ = g.noteFrameParentLocked("SEVEN", "PAGE")
				_ = g.noteChildFrameLocked("SEVEN", sevenOrigin+"/frame.html")
				under := g.frameUnderProtectedLocked("SEVEN")
				g.mu.Unlock()
				if !under {
					t.Fatal("the seven frame is not classified protected")
				}
				nestContext(g, session, 70, origin, "SEVEN", "world", "classified-seven", false)
				if !latchedOn(g, "PAGE") {
					t.Fatalf("a non-default world of %s in a frame classified protected did not lock the page", origin)
				}
				frameContentRefused(t, g, "non-default world in a classified protected frame")
			})
		}
	}
}

func TestExtensionGuardRuntimeEnablePendingStaysRefused(t *testing.T) {
	g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
	nt := guardedNestPage(t, g, false)
	nestContext(g, "N", 80, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "CHILD", "slicc harness content script", "pending-ext", false)
	frameContentRefused(t, g, "the guard's own Runtime.enable on the page has not answered")
	g.mu.Lock()
	nt.settle(false)
	g.mu.Unlock()
	nestContext(g, "N", 81, sevenOrigin, "CHILD", "", "pending-def", true)
	frameContentRefused(t, g, "the default context arrived with seven's origin")
}

func TestExtensionHiddenWorldLatchesWhenFrameLaterProtected(t *testing.T) {
	g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
	ordinaryNestPage(t, g)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "INNER", "late-inner", 90)
	if locked, pf := pageLatch(g); locked {
		t.Fatalf("an extension world in the ordinary /inner locked the page: protectedFrame=%q", pf)
	}
	g.mu.Lock()
	_ = g.noteChildFrameLocked("HOST", sevenOrigin+"/embed")
	g.queueContextSettleLocked()
	_ = g.takeContextSettlePageLocked()
	_ = g.takeContextSettleSendLocked()
	under := g.frameUnderProtectedLocked("INNER")
	g.mu.Unlock()
	if !under {
		t.Fatal("/inner is not under the host once the host is classified seven")
	}
	if !latchedOn(g, "PAGE") {
		t.Fatal("a hidden extension world whose frame became protected did not lock the page")
	}
	frameContentRefused(t, g, "hidden world under a host that became protected")
}

func TestExtensionSevenOriginIsolatedWorldLatchesUnclassifiedFrame(t *testing.T) {
	g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
	guardedNestPage(t, g, true)
	nestContext(g, "N", 95, sevenOrigin, "CHILD", "__puppeteer_utility_world__", "seven-iso", false)
	if !latchedOn(g, "PAGE") {
		t.Fatal("an isolated world with seven's origin in an unclassified frame did not lock the page")
	}
}

func TestExtensionWorldsInMiddleIframeChildrenKeepPageReadable(t *testing.T) {
	g := newGuard(protect.MustNew([]string{sevenOrigin, "chrome-extension://sevenextid"}), t.Logf)
	guardedNestPage(t, g, true)
	guardedMiddle(t, g)
	nestContext(g, "N", 100, "http://localhost:7002", "PAGE", "", "page-def", true)
	nestContext(g, "N", 107, "http://localhost:7002", "HOST", "", "host-def", true)
	nestContext(g, "NM", 101, "http://localhost:7003", "MID", "", "mid-def", true)
	nestContext(g, "NM", 102, "http://localhost:7003", "MID-INNER", "", "mid-inner-def", true)
	nestContext(g, "NM", 103, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "MID-INNER", "slicc harness content script", "mid-inner-ext", false)
	nestContext(g, "NM", 104, "chrome-extension://sevenextid", "MID-INNER", "seven content script", "mid-inner-seven-ext", false)
	nestContext(g, "NM", 105, "http://localhost:7003", "MID-INNER", "slicc-ext-probe-0123456789abcdef", "mid-inner-probe", false)
	nestContext(g, "N", 106, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "HOST", "slicc harness content script", "host-ext", false)
	if latchedOn(g, "MID") || latchedOn(g, "PAGE") {
		t.Fatalf("extension worlds in ordinary child frames locked the middle iframe or the page: mid=%v page=%v", latchedOn(g, "MID"), latchedOn(g, "PAGE"))
	}
	frameContentOpen(t, g, "extension worlds in ordinary child frames of the middle iframe")
}

func TestExtensionSevenInheritedChildUnclassifiedDefaultLatchesOnExpiry(t *testing.T) {
	t.Setenv(HoldTimeoutEnv, "1ms")
	g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
	guardedNestPage(t, g, true)
	nestContext(g, "S", 110, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "CHILD", "slicc harness content script", "expiry-ext", false)
	nestContext(g, "S", 111, "http://localhost:7002", "CHILD", "", "expiry-def", true)
	g.mu.Lock()
	held := g.contextHeldLocked("PAGE", 111)
	g.mu.Unlock()
	if !held {
		t.Fatal("the default context of an unclassified child was not held")
	}
	time.Sleep(5 * time.Millisecond)
	g.mu.Lock()
	g.queueContextSettleLocked()
	_ = g.takeContextSettlePageLocked()
	_ = g.takeContextSettleSendLocked()
	g.mu.Unlock()
	if !latchedOn(g, "PAGE") {
		t.Fatal("a default context whose frame never classified did not lock the page once its hold ran out")
	}
	frameContentRefused(t, g, "unclassified child whose default context timed out")
}

func guardSessionNest(t *testing.T, host, hostOrigin string) *guard {
	t.Helper()
	g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
	guardedNestPage(t, g, true)
	g.fromBrowser(encode(map[string]any{"method": "Page.frameAttached", "sessionId": "N", "params": map[string]any{"frameId": "HOST", "parentFrameId": "PAGE"}}))
	g.fromBrowser(encode(map[string]any{"method": "Page.frameAttached", "sessionId": "N", "params": map[string]any{"frameId": "INNER", "parentFrameId": "HOST"}}))
	nestContext(g, "N", 120, "http://localhost:7002", "PAGE", "", "gs-page", true)
	nestContext(g, "N", 121, host, "HOST", "", "gs-host", true)
	nestContext(g, "N", 122, "http://localhost:7002", "INNER", "", "gs-inner", true)
	nestContext(g, "N", 123, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "HOST", "slicc harness content script", "gs-host-ext", false)
	nestContext(g, "N", 124, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "INNER", "slicc harness content script", "gs-inner-ext", false)
	v := nestCommand(g, "S", "Page.getFrameTree", nil)
	if v.forward == nil {
		if host == sevenOrigin {
			return g
		}
		t.Fatalf("Page.getFrameTree on the ordinary nest page: reply=%s", v.reply)
	}
	g.fromBrowser(encode(map[string]any{"id": nestCalls, "sessionId": "S", "result": map[string]any{"frameTree": map[string]any{
		"frame": map[string]any{"id": "PAGE", "url": "http://localhost:7002/nest", "securityOrigin": "http://localhost:7002"},
		"childFrames": []any{map[string]any{"frame": map[string]any{"id": "HOST", "parentId": "PAGE", "url": "about:blank", "securityOrigin": hostOrigin}, "childFrames": []any{
			map[string]any{"frame": map[string]any{"id": "INNER", "parentId": "HOST", "url": "http://localhost:7002/inner", "securityOrigin": "http://localhost:7002"}},
		}}},
	}}}))
	return g
}

func TestExtensionGuardSessionDefaultClassifiesBlankHost(t *testing.T) {
	for _, hostOrigin := range []string{"://", ""} {
		t.Run("tree origin "+hostOrigin, func(t *testing.T) {
			g := guardSessionNest(t, "http://localhost:7002", hostOrigin)
			w := nestCommand(g, "S", "Page.createIsolatedWorld", map[string]any{"frameId": "INNER", "worldName": "client-world"})
			if w.wait != nil || w.reply != nil || w.forward == nil {
				t.Fatalf("Page.createIsolatedWorld on /inner under an ordinary about:blank host: wait=%v forward=%v reply=%s", w.wait != nil, w.forward != nil, w.reply)
			}
			if latchedOn(g, "PAGE") {
				t.Fatal("the ordinary nest page is locked")
			}
		})
	}
}

func TestExtensionGuardSessionSevenDefaultRefusesBlankHost(t *testing.T) {
	for _, hostOrigin := range []string{"://", "", sevenOrigin} {
		t.Run("tree origin "+hostOrigin, func(t *testing.T) {
			g := guardSessionNest(t, sevenOrigin, hostOrigin)
			w := nestCommand(g, "S", "Page.createIsolatedWorld", map[string]any{"frameId": "INNER", "worldName": "client-world"})
			if w.forward != nil || w.reply == nil {
				t.Fatalf("Page.createIsolatedWorld under seven's about:blank child: wait=%v forward=%v reply=%s", w.wait != nil, w.forward != nil, w.reply)
			}
			if !latchedOn(g, "PAGE") {
				t.Fatal("seven's about:blank child did not lock the page")
			}
			frameContentRefused(t, g, "seven's about:blank child seen on the guard session")
		})
	}
}

const delayedChildText = "seven-child-secret-7f3a"

func delayedChildDocument(id int64, frame string) []byte {
	return encode(map[string]any{"id": id, "sessionId": "S", "result": map[string]any{"root": map[string]any{
		"nodeId": 1, "backendNodeId": 1, "nodeType": 9, "nodeName": "#document", "documentURL": "http://localhost:7002/nest", "frameId": "PAGE",
		"children": []any{map[string]any{"nodeId": 2, "backendNodeId": 2, "nodeType": 1, "nodeName": "IFRAME", "frameId": frame,
			"contentDocument": map[string]any{"nodeId": 3, "backendNodeId": 3, "nodeType": 9, "nodeName": "#document", "documentURL": "about:blank", "frameId": frame,
				"children": []any{map[string]any{"nodeId": 4, "backendNodeId": 4, "nodeType": 3, "nodeName": "#text", "nodeValue": delayedChildText}}}}},
	}}})
}

func clientSees(v browserVerdict, text string) bool {
	if strings.Contains(string(v.forward), text) {
		return true
	}
	for _, p := range v.page {
		if strings.Contains(string(p), text) {
			return true
		}
	}
	return false
}

func nestClientSeesDocument(g *guard, id int64, frame, text string) bool {
	return clientSees(nestSettle(g, g.fromBrowser(delayedChildDocument(id, frame)), nestOrdinaryURL), text)
}

func TestExtensionDelayedDefaultProtectedChildCommandsRefused(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			t.Run(kind+"/"+session, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
				guardedNestPage(t, g, true)
				frame := "CHILD-" + kind
				nestContext(g, session, 130, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "delayed-ext-"+kind, false)
				if !latchedOn(g, "PAGE") {
					t.Fatal("a non-default world in a frame with no known parent, origin or default context did not lock the page")
				}
				doc := nestCommand(g, "S", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true})
				if doc.forward != nil {
					if nestClientSeesDocument(g, nestCalls, frame, delayedChildText) {
						t.Errorf("DOM.getDocument pierce showed the child's text before its default context arrived")
					}
				} else if doc.reply == nil || !strings.Contains(string(doc.reply), "refused") {
					t.Errorf("DOM.getDocument pierce must be refused before the child's default context arrives: reply=%s", doc.reply)
				}
				for _, method := range []string{"Page.captureScreenshot", "Page.startScreencast", "DOMSnapshot.captureSnapshot", "Accessibility.getFullAXTree"} {
					v := nestCommand(g, "S", method, nil)
					if v.forward != nil || v.reply == nil || !strings.Contains(string(v.reply), "refused") {
						t.Errorf("%s must be refused while the child's default context has not arrived: forward=%v reply=%s", method, v.forward != nil, v.reply)
					}
				}
				nestContext(g, session, 131, sevenOrigin, frame, "", "delayed-def-"+kind, true)
				if !latchedOn(g, "PAGE") {
					t.Fatal("the delayed default context with seven's origin did not lock the page")
				}
				frameContentRefused(t, g, "after the delayed default context arrived")
			})
		}
	}
}

func frameContentEvents(text string) []map[string]any {
	return []map[string]any{
		{"method": "DOM.setChildNodes", "sessionId": "S", "params": map[string]any{"parentId": 3, "nodes": []any{map[string]any{"nodeId": 5, "backendNodeId": 5, "nodeType": 3, "nodeName": "#text", "nodeValue": text}}}},
		{"method": "DOM.characterDataModified", "sessionId": "S", "params": map[string]any{"nodeId": 4, "characterData": text}},
		{"method": "DOM.childNodeInserted", "sessionId": "S", "params": map[string]any{"parentNodeId": 3, "previousNodeId": 0, "node": map[string]any{"nodeId": 6, "backendNodeId": 6, "nodeType": 3, "nodeName": "#text", "nodeValue": text}}},
		{"method": "DOM.documentUpdated", "sessionId": "S", "params": map[string]any{}},
		{"method": "Accessibility.nodesUpdated", "sessionId": "S", "params": map[string]any{"nodes": []any{map[string]any{"nodeId": "1", "name": map[string]any{"value": text}, "value": map[string]any{"value": text}}}}},
		{"method": "Accessibility.loadComplete", "sessionId": "S", "params": map[string]any{"root": map[string]any{"nodeId": "1", "name": map[string]any{"value": text}, "value": map[string]any{"value": text}}}},
	}
}

func assertFrameContentEventsHidden(t *testing.T, g *guard, text, why string) {
	t.Helper()
	for _, ev := range frameContentEvents(text) {
		v := g.fromBrowser(encode(ev))
		method, _ := ev["method"].(string)
		if method == "DOM.documentUpdated" {
			if v.forward != nil {
				t.Errorf("%s: %s must be dropped while a latch holds", why, method)
			}
			continue
		}
		if clientSees(v, text) {
			t.Errorf("%s: %s reached the client", why, method)
		}
		if v.forward != nil {
			t.Errorf("%s: %s must be dropped, not sanitized: forward=%s", why, method, v.forward)
		}
	}
}

func assertFrameContentEventsFlow(t *testing.T, g *guard, text, why string) {
	t.Helper()
	for _, ev := range frameContentEvents(text) {
		v := g.fromBrowser(encode(ev))
		method, _ := ev["method"].(string)
		if method == "DOM.documentUpdated" {
			if v.forward == nil {
				t.Errorf("%s: %s must reach the client on an ordinary page", why, method)
			}
			continue
		}
		if !clientSees(v, text) {
			t.Errorf("%s: %s must reach the client on an ordinary page", why, method)
		}
	}
}

func pageHasMethod(pages [][]byte, method string) bool {
	for _, p := range pages {
		if strings.Contains(string(p), `"method":"`+method+`"`) {
			return true
		}
	}
	return false
}

func enableClientDOM(t *testing.T, g *guard) {
	t.Helper()
	v := nestCommand(g, "S", "DOM.enable", nil)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("DOM.enable on the client session: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}

func enableClientAX(t *testing.T, g *guard) {
	t.Helper()
	v := nestCommand(g, "S", "Accessibility.enable", nil)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("Accessibility.enable on the client session: forward=%v reply=%s", v.forward != nil, v.reply)
	}
}

func TestExtensionDelayedDefaultProtectedChildDOMEvents(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			t.Run(kind+"/"+session, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
				guardedNestPage(t, g, true)
				enableClientDOM(t, g)
				enableClientAX(t, g)
				frame := "CHILD-" + kind
				nestContext(g, session, 140, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "delayed-dom-ext-"+kind, false)
				if !latchedOn(g, "PAGE") {
					t.Fatal("a non-default world in a frame with no known parent, origin or default context did not lock the page")
				}
				assertFrameContentEventsHidden(t, g, delayedChildText, "before the child's default context arrived")
				v := nestContext(g, session, 141, sevenOrigin, frame, "", "delayed-dom-def-"+kind, true)
				if !latchedOn(g, "PAGE") {
					t.Fatal("the delayed default context with seven's origin did not lock the page")
				}
				if pageHasMethod(v.page, "DOM.documentUpdated") || pageHasMethod(v.page, "Accessibility.loadComplete") {
					t.Fatal("a protected classification emitted a DOM/AX rebuild event")
				}
				assertFrameContentEventsHidden(t, g, delayedChildText, "after the delayed default context arrived")
			})
		}
	}
}

func TestExtensionSoftLatchOrdinaryDefaultRebuildsDOM(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			t.Run(kind+"/"+session, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
				guardedNestPage(t, g, true)
				enableClientDOM(t, g)
				enableClientAX(t, g)
				frame := "CHILD-ORD-" + kind
				nestContext(g, session, 170, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "ord-ext-"+kind, false)
				if !latchedOn(g, "PAGE") {
					t.Fatal("the soft latch did not lock the page")
				}
				assertFrameContentEventsHidden(t, g, delayedChildText, "while the soft latch holds")
				v := nestContext(g, session, 171, "http://localhost:7002", frame, "", "ord-def-"+kind, true)
				if latchedOn(g, "PAGE") {
					t.Fatal("an ordinary default context did not release the soft latch")
				}
				if !pageHasMethod(v.page, "DOM.documentUpdated") {
					t.Fatal("releasing the soft latch to ordinary did not emit DOM.documentUpdated")
				}
				if !pageHasMethod(v.page, "Accessibility.loadComplete") {
					t.Fatal("releasing the soft latch to ordinary did not emit Accessibility.loadComplete after Accessibility.enable")
				}
				if pageHasMethod(v.page, delayedChildText) {
					t.Fatal("the rebuild events carried frame content")
				}
				assertFrameContentEventsFlow(t, g, "ordinary-after-release-4b1c", "after the soft latch released")
				doc := nestCommand(g, "S", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true})
				if doc.reply != nil || doc.forward == nil {
					t.Fatalf("DOM.getDocument after ordinary release: forward=%v reply=%s", doc.forward != nil, doc.reply)
				}
				if !nestClientSeesDocument(g, nestCalls, frame, delayedChildText) {
					t.Fatal("DOM.getDocument pierce after ordinary release did not show the child's text")
				}
			})
		}
	}
}

func TestExtensionSoftLatchProtectedDefaultSkipsRebuild(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			t.Run(kind+"/"+session, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
				guardedNestPage(t, g, true)
				enableClientDOM(t, g)
				enableClientAX(t, g)
				frame := "CHILD-PROT-" + kind
				nestContext(g, session, 180, "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", frame, "slicc harness content script", "prot-ext-"+kind, false)
				v := nestContext(g, session, 181, sevenOrigin, frame, "", "prot-def-"+kind, true)
				if !latchedOn(g, "PAGE") {
					t.Fatal("seven's default context did not lock the page")
				}
				if pageHasMethod(v.page, "DOM.documentUpdated") || pageHasMethod(v.page, "Accessibility.loadComplete") {
					t.Fatal("a protected default emitted a DOM/AX rebuild event")
				}
				doc := nestCommand(g, "S", "DOM.getDocument", map[string]any{"depth": -1, "pierce": true})
				if doc.forward != nil || doc.reply == nil || !strings.Contains(string(doc.reply), "refused") {
					t.Fatalf("DOM.getDocument must stay refused after a protected default: forward=%v reply=%s", doc.forward != nil, doc.reply)
				}
			})
		}
	}
}

func TestExtensionProtectedFrameDOMEventsDropped(t *testing.T) {
	for _, kind := range []string{"blank", "srcdoc"} {
		for _, session := range []string{"N", "S"} {
			t.Run(kind+"/"+session, func(t *testing.T) {
				g := newGuard(protect.MustNew([]string{sevenOrigin}), t.Logf)
				guardedNestPage(t, g, true)
				frame := "CHILD-" + kind
				nestContext(g, session, 150, sevenOrigin, frame, "", "prot-def-"+kind, true)
				if !latchedOn(g, "PAGE") {
					t.Fatal("seven's default context did not lock the page")
				}
				assertFrameContentEventsHidden(t, g, delayedChildText, "while a frame under protection holds a latch")
			})
		}
	}
}

func TestExtensionOrdinaryPageDOMEventsKeepFlowing(t *testing.T) {
	g := newGuard(protect.MustNew([]string{sevenOrigin, "chrome-extension://sevenextid"}), t.Logf)
	ordinaryNestPage(t, g)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "HOST", "ord-host-ext", 160)
	injectExtensionWorld(g, "S", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob", "INNER", "ord-inner-ext", 161)
	if latchedOn(g, "PAGE") {
		t.Fatal("extension worlds on an ordinary nest page locked it")
	}
	assertFrameContentEventsFlow(t, g, "ordinary-child-text-9c2e", "ordinary nest page with extension worlds")
}
