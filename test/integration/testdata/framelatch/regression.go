package cdp

import (
	"encoding/json"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	flExtensionURL = "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"
	flSeven        = "https://seven.sliccy.ai"
)

func flGuard(t *testing.T, document string) *guard {
	t.Helper()
	o, err := protect.New([]string{flSeven, "chrome-extension://abcdefghijklmnopabcdefghijklmnop"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	revision := uint64(0)
	if document != "" {
		revision = 1
	}
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/", ctx: "default", document: document, documentRevision: revision}
	g.targets["EXT"] = &target{id: "EXT", typ: "iframe", url: flExtensionURL, ctx: "default", parentFrame: "PAGE"}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	g.noteExtensionFrameURLLocked("PAGE", "EXT", flExtensionURL)
	return g
}

func flContext(t *testing.T, g *guard, id int64, frame, origin string, isDefault bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": id, "origin": origin, "name": "", "uniqueId": "U" + itoa(id),
			"auxData": map[string]any{"isDefault": isDefault, "frameId": frame},
		}},
	})
	g.fromBrowser(raw)
}

var flKinds = []string{"default", "world"}

func flAttach(t *testing.T, g *guard, frame string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"method": "Page.frameAttached", "sessionId": "S",
		"params": map[string]any{"frameId": frame, "parentFrameId": "PAGE"},
	})
	g.fromBrowser(raw)
	g.mu.Lock()
	g.childFrames[frame] = "https://ordinary.test/child"
	g.mu.Unlock()
}

func flOlderLatch(t *testing.T, g *guard, kind string) {
	t.Helper()
	if kind == "world" {
		flAttach(t, g, "CHILD")
		flContext(t, g, 7, "CHILD", flSeven, false)
	} else {
		flContext(t, g, 7, "CHILD", flSeven, true)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.worlds["PAGE"]
	if w == nil || w.contexts[7] == nil || w.contexts[7].protectedFrame != "CHILD" {
		t.Fatalf("older classification did not latch the child frame: %+v", w)
	}
	if g.iframeProtectedLocked("PAGE") || g.targets["PAGE"].childDocDOMBlocked || g.unknownNonDefaultWorldProtectsLocked("PAGE") {
		t.Fatal("child frame must be protected only through the world latch")
	}
	if kind == "world" && g.frameUnderProtectedLocked("CHILD") {
		t.Fatal("world kind must not rest on a protected frame record")
	}
	if !g.framesProtectedLocked("PAGE") {
		t.Fatal("world latch must protect the page's frames")
	}
}

type flCall struct {
	ID        int64          `json:"id"`
	Method    string         `json:"method"`
	SessionID string         `json:"sessionId"`
	Params    map[string]any `json:"params"`
}

func flReply(call flCall, loader string) string {
	switch call.Method {
	case "Page.getFrameTree":
		return `{"frameTree":{"frame":{"id":"PAGE","loaderId":"` + loader + `","url":"https://ordinary.test/"}}}`
	case "DOM.getDocument":
		return `{"root":{"nodeId":1,"nodeName":"#document","documentURL":"https://ordinary.test/"}}`
	case "Page.createIsolatedWorld":
		return `{"executionContextId":420}`
	case "DOM.getFrameOwner":
		return `{"backendNodeId":5,"nodeId":0}`
	case "DOM.resolveNode":
		return `{"object":{"type":"object","objectId":"OBJ-1"}}`
	case "Runtime.callFunctionOn":
		return `{"result":{"type":"boolean","value":false}}`
	}
	return `{}`
}

func flRunProbe(t *testing.T, g *guard, c *command, loader string) (pageVerdict, []string) {
	t.Helper()
	var shown, ok, finished bool
	g.mu.Lock()
	page := g.sessionTargetLocked(c.session)
	frames := g.extensionFrameIDsLocked(page.id)
	queue := g.probeExtensionShownLocked(c.session, page.id, frames, func(s, o bool) [][]byte {
		shown, ok, finished = s, o, true
		return nil
	})
	g.mu.Unlock()
	var methods []string
	for i := 0; i < len(queue) && i < 64; i++ {
		var call flCall
		if json.Unmarshal(queue[i], &call) != nil || call.Method == "" {
			continue
		}
		methods = append(methods, call.Method)
		if call.Method == "Runtime.releaseObjectGroup" {
			continue
		}
		reply := `{"id":` + itoa(call.ID) + `,"sessionId":"` + call.SessionID + `","result":` + flReply(call, loader) + `}`
		v := g.fromBrowser([]byte(reply))
		queue = append(queue, v.send...)
	}
	if !finished {
		t.Fatalf("probe did not finish: %v", methods)
	}
	return g.afterExtensionShown(c, shown, ok), methods
}

func flCommand(id int64, method string, params map[string]any) *command {
	if params == nil {
		params = map[string]any{}
	}
	return &command{id: id, method: method, session: "S", params: params}
}

const flExtensionWorldOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func flRefusal(id int64) string {
	return `{"error":{"code":-32000,"message":"slicc: refused: target is on seven's origin"},"id":` + itoa(id) + `,"sessionId":"S"}`
}

func flLatch(g *guard, id int64) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.worlds["PAGE"]
	if w == nil || w.contexts[id] == nil {
		return "", false
	}
	return w.contexts[id].protectedFrame, true
}

func flProtected(g *guard) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.framesProtectedLocked("PAGE")
}

func flCommit(t *testing.T, g *guard, loader string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"method": "Page.frameNavigated", "sessionId": "S",
		"params": map[string]any{"frame": map[string]any{"id": "PAGE", "loaderId": loader, "url": "https://ordinary.test/next", "securityOrigin": "https://ordinary.test"}, "type": "Navigation"},
	})
	g.fromBrowser(raw)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.targets["PAGE"].document != loader {
		t.Fatalf("top-level commit not recorded: document=%q", g.targets["PAGE"].document)
	}
}

func flMark(t *testing.T, g *guard, frame string) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.applyChildDocMarksLocked("S", &childDocStripState{origins: g.o, session: "S", hitProtected: true, markFrames: []string{frame}})
	if !g.targets["PAGE"].childDocDOMBlocked {
		t.Fatal("mark did not block the page")
	}
}

func flMarkable(t *testing.T, g *guard, id int64, frame string) {
	t.Helper()
	flAttach(t, g, frame)
	flContext(t, g, id, frame, flExtensionWorldOrigin, false)
	if latch, ok := flLatch(g, id); !ok || latch != "" {
		t.Fatalf("context %d must be tracked without a latch: tracked=%t latch=%q", id, ok, latch)
	}
	if flProtected(g) {
		t.Fatal("page must start unprotected")
	}
}

var flMouse = map[string]any{"type": "mouseMoved", "x": 1, "y": 1}

func flAsk(t *testing.T, g *guard, id int64, method string) pageVerdict {
	t.Helper()
	params := map[string]any{}
	if method == "Input.dispatchMouseEvent" {
		params = flMouse
	}
	c := flCommand(id, method, params)
	v := g.fromCommand(c, c.id, true)
	if v.extension != nil {
		v, _ = flRunProbe(t, g, c, "L9")
	}
	return v
}

func TestFrameLatchMarkClearsOnTopLevelCommit(t *testing.T) {
	for _, commit := range flCommits[:2] {
		t.Run(commit.name, func(t *testing.T) {
			g := flGuard(t, commit.document)
			flMarkable(t, g, 9, "MCHILD")
			flMark(t, g, "MCHILD")
			if latch, _ := flLatch(g, 9); latch != "MCHILD" {
				t.Fatalf("mark did not latch its frame: %q", latch)
			}
			for i, method := range []string{"DOM.getDocument", "DOMSnapshot.captureSnapshot", "Accessibility.getFullAXTree", "Page.getFrameTree"} {
				if v := flAsk(t, g, int64(70+i), method); string(v.reply) != flRefusal(int64(70+i)) {
					t.Fatalf("%s before the commit: want %s, got reply=%s forward=%s", method, flRefusal(int64(70+i)), v.reply, v.forward)
				}
			}
			flCommit(t, g, commit.loader)
			g.mu.Lock()
			blocked := g.targets["PAGE"].childDocDOMBlocked
			g.mu.Unlock()
			if blocked {
				t.Fatal("page-level mark must clear on a top-level commit")
			}
			if latch, _ := flLatch(g, 9); latch != "" {
				t.Fatalf("mark latch must clear on a top-level commit: %q", latch)
			}
			if flProtected(g) {
				t.Fatal("page must be unprotected once the mark clears")
			}
			for i, method := range []string{"DOM.getDocument", "DOMSnapshot.captureSnapshot", "Accessibility.getFullAXTree", "Page.getFrameTree", "Page.captureScreenshot", "Input.dispatchMouseEvent"} {
				if v := flAsk(t, g, int64(80+i), method); v.forward == nil {
					t.Fatalf("%s after the commit must be forwarded: reply=%s", method, v.reply)
				}
			}
		})
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-clears PASS")
	}
}

func TestFrameLatchMarkSurvivesShownProbe(t *testing.T) {
	g := flGuard(t, "")
	flMarkable(t, g, 9, "MCHILD")
	flMark(t, g, "MCHILD")
	c := flCommand(33, "Input.dispatchMouseEvent", flMouse)
	if v := g.fromCommand(c, c.id, true); v.extension == nil {
		t.Fatalf("shown method must wait for the probe: reply=%s forward=%s", v.reply, v.forward)
	}
	after, methods := flRunProbe(t, g, c, "L2")
	if len(methods) == 0 || methods[0] != "Page.getFrameTree" {
		t.Fatalf("probe must learn the root document first: %v", methods)
	}
	g.mu.Lock()
	document := g.targets["PAGE"].document
	blocked := g.targets["PAGE"].childDocDOMBlocked
	g.mu.Unlock()
	if document != "L2" {
		t.Fatalf("probe did not record the root document: %q", document)
	}
	if !blocked {
		t.Fatal("page-level mark must survive the probe")
	}
	if latch, _ := flLatch(g, 9); latch != "MCHILD" {
		t.Fatalf("mark latch must survive the probe: %q", latch)
	}
	if string(after.reply) != flRefusal(33) {
		t.Fatalf("after the probe: want %s, got reply=%s forward=%s", flRefusal(33), after.reply, after.forward)
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-survives-probe PASS")
	}
}

var flCommits = []struct{ name, document, loader string }{
	{"first-commit", "", "L2"},
	{"new-document", "L1", "L2"},
	{"same-document", "L1", "L1"},
}

func TestFrameLatchOlderSurvivesTopLevelCommit(t *testing.T) {
	for _, kind := range flKinds {
		for _, commit := range flCommits {
			t.Run(kind+"/"+commit.name, func(t *testing.T) {
				g := flGuard(t, commit.document)
				flOlderLatch(t, g, kind)
				flCommit(t, g, commit.loader)
				if latch, _ := flLatch(g, 7); latch != "CHILD" {
					t.Fatalf("older latch must survive a top-level commit: %q", latch)
				}
				if !flProtected(g) {
					t.Fatal("page must stay protected")
				}
				for i, method := range []string{"DOM.getDocument", "DOMSnapshot.captureSnapshot", "Page.getFrameTree", "Input.dispatchMouseEvent"} {
					if v := flAsk(t, g, int64(90+i), method); v.forward != nil || string(v.reply) != flRefusal(int64(90+i)) {
						t.Fatalf("%s after the commit: want %s, got reply=%s forward=%s", method, flRefusal(int64(90+i)), v.reply, v.forward)
					}
				}
			})
		}
	}
	if !t.Failed() {
		t.Log("row framelatch-older-survives-commit PASS")
	}
}

func TestFrameLatchMarkSurvivesSameDocumentCommit(t *testing.T) {
	for _, path := range []string{"committed", "probe-learned"} {
		t.Run(path, func(t *testing.T) {
			document := "L1"
			if path == "probe-learned" {
				document = ""
			}
			g := flGuard(t, document)
			flMarkable(t, g, 9, "MCHILD")
			flMark(t, g, "MCHILD")
			if path == "probe-learned" {
				c := flCommand(34, "Input.dispatchMouseEvent", flMouse)
				if v := g.fromCommand(c, c.id, true); v.extension == nil {
					t.Fatalf("shown method must wait for the probe: reply=%s forward=%s", v.reply, v.forward)
				}
				flRunProbe(t, g, c, "L1")
			}
			flCommit(t, g, "L1")
			g.mu.Lock()
			blocked := g.targets["PAGE"].childDocDOMBlocked
			g.mu.Unlock()
			if !blocked {
				t.Fatal("page-level mark must survive a commit of the same document")
			}
			if latch, _ := flLatch(g, 9); latch != "MCHILD" {
				t.Fatalf("mark latch must survive a commit of the same document: %q", latch)
			}
			for i, method := range []string{"DOM.getDocument", "DOMSnapshot.captureSnapshot", "Accessibility.getFullAXTree", "Page.getFrameTree", "Page.captureScreenshot", "Input.dispatchMouseEvent"} {
				if v := flAsk(t, g, int64(100+i), method); v.forward != nil || string(v.reply) != flRefusal(int64(100+i)) {
					t.Fatalf("%s after the commit: want %s, got reply=%s forward=%s", method, flRefusal(int64(100+i)), v.reply, v.forward)
				}
			}
		})
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-survives-same-document PASS")
	}
}

func TestFrameLatchOlderSurvivesShownProbe(t *testing.T) {
	for _, kind := range flKinds {
		t.Run(kind, func(t *testing.T) {
			g := flGuard(t, "")
			flOlderLatch(t, g, kind)
			c := flCommand(30, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 1})
			if v := g.fromCommand(c, c.id, true); v.extension == nil {
				t.Fatalf("shown method must wait for the probe: reply=%s forward=%s", v.reply, v.forward)
			}
			_, methods := flRunProbe(t, g, c, "L2")
			if len(methods) == 0 || methods[0] != "Page.getFrameTree" {
				t.Fatalf("probe must learn the root document first: %v", methods)
			}
			g.mu.Lock()
			document := g.targets["PAGE"].document
			g.mu.Unlock()
			if document != "L2" {
				t.Fatalf("probe did not record the root document: %q", document)
			}
			if latch, _ := flLatch(g, 7); latch != "CHILD" {
				t.Fatalf("older latch must survive the probe: %q", latch)
			}
			if !flProtected(g) {
				t.Fatal("page must stay protected")
			}
		})
	}
	if !t.Failed() {
		t.Log("row framelatch-older-survives-probe PASS")
	}
}

func TestFrameLatchMarkKeepsOlderOwner(t *testing.T) {
	for _, kind := range flKinds {
		for _, path := range []string{"commit", "probe"} {
			flMarkKeepsOlder(t, kind, path)
		}
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-keeps-older PASS")
	}
}

func flMarkKeepsOlder(t *testing.T, kind, path string) {
	t.Run(kind+"/"+path, func(t *testing.T) {
		g := flGuard(t, "")
		flOlderLatch(t, g, kind)
		flMark(t, g, "CHILD")
		if path == "commit" {
			flCommit(t, g, "L2")
		} else {
			c := flCommand(31, "Page.captureScreenshot", nil)
			if v := g.fromCommand(c, c.id, true); v.extension == nil {
				t.Fatalf("screenshot must wait for the probe: reply=%s", v.reply)
			}
			flRunProbe(t, g, c, "L2")
		}
		g.mu.Lock()
		blocked := g.targets["PAGE"].childDocDOMBlocked
		g.mu.Unlock()
		if blocked != (path == "probe") {
			t.Fatalf("page-level mark after %s: blocked=%t", path, blocked)
		}
		if latch, _ := flLatch(g, 7); latch != "CHILD" {
			t.Fatalf("older latch must remain after the mark clears: %q", latch)
		}
		if !flProtected(g) {
			t.Fatal("page must stay protected")
		}
	})
}

func TestFrameLatchOlderTakesOverMarked(t *testing.T) {
	t.Run("context-created", func(t *testing.T) {
		g := flGuard(t, "L1")
		flMarkable(t, g, 9, "MCHILD")
		flMark(t, g, "MCHILD")
		flContext(t, g, 9, "MCHILD", flSeven, false)
		flCommit(t, g, "L2")
		if latch, _ := flLatch(g, 9); latch != "MCHILD" {
			t.Fatalf("latch taken over by the older classification must survive: %q", latch)
		}
	})
	t.Run("frame-protected", func(t *testing.T) {
		g := flGuard(t, "")
		flMarkable(t, g, 9, "MCHILD")
		flMark(t, g, "MCHILD")
		g.mu.Lock()
		g.childFrames["MCHILD"] = flSeven + "/embed"
		g.mu.Unlock()
		flCommit(t, g, "L2")
		if v := flAsk(t, g, 32, "Input.dispatchMouseEvent"); string(v.reply) != flRefusal(32) {
			t.Fatalf("after the commit: want %s, got reply=%s forward=%s", flRefusal(32), v.reply, v.forward)
		}
		if latch, _ := flLatch(g, 9); latch != "MCHILD" {
			t.Fatalf("latch taken over by the older classification must survive: %q", latch)
		}
	})
	if !t.Failed() {
		t.Log("row framelatch-older-takes-over PASS")
	}
}

func TestFrameLatchMarkLeavesOlderValue(t *testing.T) {
	g := flGuard(t, "L1")
	flOlderLatch(t, g, "world")
	flAttach(t, g, "OTHER")
	flContext(t, g, 7, "OTHER", flExtensionWorldOrigin, false)
	g.mu.Lock()
	frame := g.worlds["PAGE"].contexts[7].frame
	g.mu.Unlock()
	if latch, _ := flLatch(g, 7); latch != "CHILD" || frame != "OTHER" {
		t.Fatalf("context must move to OTHER and keep its older latch: frame=%q latch=%q", frame, latch)
	}
	flMark(t, g, "OTHER")
	if latch, _ := flLatch(g, 7); latch != "CHILD" {
		t.Fatalf("mark must leave the older latch value: %q", latch)
	}
	named := &command{method: "Page.createIsolatedWorld", params: map[string]any{"frameId": "CHILD", "worldName": "w"}}
	g.mu.Lock()
	reason := g.frameContentCheckLocked(named, "PAGE")
	g.mu.Unlock()
	if reason != refusedTarget {
		t.Fatalf("command naming the older latched frame: want %q, got %q", refusedTarget, reason)
	}
	flCommit(t, g, "L2")
	if latch, _ := flLatch(g, 7); latch != "CHILD" {
		t.Fatalf("older latch must survive the commit: %q", latch)
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-leaves-older-value PASS")
	}
}

func TestFrameLatchShownProbeRefusesChild(t *testing.T) {
	for _, kind := range flKinds {
		for _, document := range []string{"", "L1"} {
			flProbeRefuses(t, kind+"/document="+document, document, func(t *testing.T, g *guard) { flOlderLatch(t, g, kind) })
		}
	}
	if !t.Failed() {
		t.Log("row framelatch-probe-refuses PASS")
	}
}

func TestFrameLatchMarkShownProbeRefuses(t *testing.T) {
	for _, document := range []string{"", "L1"} {
		flProbeRefuses(t, "document="+document, document, func(t *testing.T, g *guard) {
			flMarkable(t, g, 9, "MCHILD")
			flMark(t, g, "MCHILD")
		})
	}
	if !t.Failed() {
		t.Log("row framelatch-mark-probe-refuses PASS")
	}
}

func flProbeRefuses(t *testing.T, name, document string, setup func(*testing.T, *guard)) {
	t.Run(name, func(t *testing.T) {
		g := flGuard(t, document)
		setup(t, g)
		pre := flCommand(40, "DOM.getDocument", nil)
		if v := g.fromCommand(pre, pre.id, true); string(v.reply) != flRefusal(40) {
			t.Fatalf("DOM.getDocument before the probe: want %s, got reply=%s forward=%s", flRefusal(40), v.reply, v.forward)
		}
		shown := []*command{
			flCommand(41, "Page.captureScreenshot", nil),
			flCommand(42, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 1}),
		}
		for _, c := range shown {
			v := g.fromCommand(c, c.id, true)
			if v.extension == nil {
				t.Fatalf("%s before the probe must wait for it: reply=%s forward=%s", c.method, v.reply, v.forward)
			}
			after, _ := flRunProbe(t, g, c, "L2")
			if after.forward != nil || string(after.reply) != flRefusal(c.id) {
				t.Fatalf("%s after the probe: want %s, got reply=%s forward=%s", c.method, flRefusal(c.id), after.reply, after.forward)
			}
		}
		for i, method := range []string{"DOM.getDocument", "DOMSnapshot.captureSnapshot", "Page.captureScreenshot"} {
			c := flCommand(int64(50+i), method, nil)
			v := g.fromCommand(c, c.id, true)
			if v.extension != nil {
				v, _ = flRunProbe(t, g, c, "L2")
			}
			if v.forward != nil || string(v.reply) != flRefusal(c.id) {
				t.Fatalf("later %s: want %s, got reply=%s forward=%s", method, flRefusal(c.id), v.reply, v.forward)
			}
		}
	})
}
