package cdp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	extensionFrameFixForeignURL = "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"
	extensionFrameFixNode       = `{"nodeName":"IFRAME","frameId":"FRAME","nodeId":2,"backendNodeId":2}`
)

func extensionFrameFixGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/", ctx: "default", document: "L1", documentRevision: 1}
	g.targets["FRAME"] = &target{id: "FRAME", typ: "iframe", url: extensionFrameFixForeignURL, ctx: "default", parentFrame: "PAGE"}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true}
	g.noteExtensionFrameURLLocked("PAGE", "FRAME", extensionFrameFixForeignURL)
	return g
}

func TestExtensionFrameEvaluateResultStaysUnmarked(t *testing.T) {
	g := extensionFrameFixGuard(t)
	raw := []byte(`{"id":9,"sessionId":"S","result":{"result":{"type":"object","value":` + extensionFrameFixNode + `}}}`)
	out := g.scrub(append([]byte(nil), raw...))
	if strings.Contains(string(out), `"sliccExtensionFrame"`) {
		t.Fatalf("Runtime.evaluate-shaped reply was marked: %s", out)
	}
}

func TestExtensionFrameDocumentReplyMarksOwnerNode(t *testing.T) {
	g := extensionFrameFixGuard(t)
	raw, _ := json.Marshal(map[string]any{"id": 3, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": 0}})
	c, n, ok := parseCommand(raw)
	if !ok {
		t.Fatal("parse DOM.getDocument")
	}
	if v := g.fromCommand(c, n, ok); v.forward == nil {
		t.Fatalf("DOM.getDocument refused: %s", v.reply)
	}
	reply := []byte(`{"id":3,"sessionId":"S","result":{"root":{"nodeName":"#document","children":[` + extensionFrameFixNode + `]}}}`)
	v := g.fromBrowser(reply)
	if v.forward == nil {
		t.Fatal("getDocument reply dropped")
	}
	out := g.scrub(append([]byte(nil), v.forward...))
	if !strings.Contains(string(out), `"sliccExtensionFrame"`) {
		t.Fatalf("getDocument owner node unmarked: %s", out)
	}
}

func TestExtensionFrameStripInnerReceivedMessage(t *testing.T) {
	g := extensionFrameFixGuard(t)
	inner := `{"id":1,"result":{"nodeName":"IFRAME","frameId":"FRAME","sliccExtensionFrame":{"version":1,"kind":"extensionFrame","attacker":"poisoned"}}}`
	raw, _ := json.Marshal(map[string]any{
		"method": "Target.receivedMessageFromTarget",
		"params": map[string]any{"sessionId": "NESTED", "message": inner},
	})
	out := g.scrub(raw)
	if strings.Contains(string(out), `"attacker"`) || strings.Contains(string(out), `"sliccExtensionFrame"`) {
		t.Fatalf("inner receivedMessageFromTarget marker survived: %s", out)
	}
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		t.Fatalf("scrubbed message is not JSON: %s", out)
	}
	params, _ := m["params"].(map[string]any)
	msg, _ := params["message"].(string)
	if msg == "" || strings.Contains(msg, "sliccExtensionFrame") {
		t.Fatalf("inner message not stripped: %v", params)
	}
}

func TestExtensionProbeConcurrentFirstWorldShared(t *testing.T) {
	g := extensionFrameFixGuard(t)
	var finished int
	done := func(shown, ok bool) [][]byte {
		finished++
		return nil
	}
	first := g.probeExtensionShownLocked("S", "PAGE", []string{"FRAME"}, done)
	second := g.probeExtensionShownLocked("S", "PAGE", []string{"FRAME"}, done)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected one getDocument each: first=%d second=%d", len(first), len(second))
	}
	var firstCall, secondCall struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal(first[0], &firstCall) != nil || firstCall.Method != "DOM.getDocument" {
		t.Fatalf("first probe: %s", first[0])
	}
	if json.Unmarshal(second[0], &secondCall) != nil || secondCall.Method != "DOM.getDocument" {
		t.Fatalf("second probe: %s", second[0])
	}
	a := g.fromBrowser([]byte(`{"id":` + itoa(firstCall.ID) + `,"sessionId":"S","result":{"root":{"nodeId":1}}}`))
	b := g.fromBrowser([]byte(`{"id":` + itoa(secondCall.ID) + `,"sessionId":"S","result":{"root":{"nodeId":1}}}`))
	creates := 0
	var worldID int64
	for _, batch := range [][][]byte{a.send, b.send} {
		for _, msg := range batch {
			var m struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(msg, &m) != nil {
				continue
			}
			if m.Method == "Page.createIsolatedWorld" {
				creates++
				worldID = m.ID
			}
		}
	}
	if creates != 1 {
		t.Fatalf("concurrent first probes created %d worlds, want 1", creates)
	}
	if worldID == 0 {
		t.Fatal("missing createIsolatedWorld id")
	}
	world := g.fromBrowser([]byte(`{"id":` + itoa(worldID) + `,"sessionId":"S","result":{"executionContextId":42}}`))
	owners := 0
	for _, msg := range world.send {
		var m struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(msg, &m) == nil && m.Method == "DOM.getFrameOwner" {
			owners++
		}
	}
	if owners != 2 {
		t.Fatalf("after shared world, getFrameOwner count=%d want 2; send=%v", owners, world.send)
	}
	if finished != 0 {
		t.Fatalf("probes finished early: %d", finished)
	}
}

func TestExtensionPaintReleasedOnSessionDetach(t *testing.T) {
	t.Setenv(extensionPaintHoldEnv, "30s")
	g := extensionFrameFixGuard(t)
	held := 0
	g.reserveHold = func(data []byte) (*link.Reservation, bool) {
		held++
		return nil, true
	}
	id := int64(11)
	m := message{ID: &id, SessionID: "S"}
	data := []byte(`{"id":11,"sessionId":"S","result":{"data":"YWFh"}}`)
	g.mu.Lock()
	v := g.holdExtensionPaintLocked(&m, data, "PAGE", []string{"FRAME"})
	g.mu.Unlock()
	if v.forward != nil || len(v.extensionPaints) != 1 || held != 1 {
		t.Fatalf("hold failed: forward=%s paints=%d held=%d", v.forward, len(v.extensionPaints), held)
	}
	if _, ok := g.heldExtensionPaints[callKey{"S", 11}]; !ok {
		t.Fatal("expected a held extension paint")
	}
	start := time.Now()
	g.mu.Lock()
	g.forgetSessionLocked("S")
	page := g.takePendingPageLocked()
	_ = g.takeReleasesLocked()
	g.mu.Unlock()
	if time.Since(start) > time.Second {
		t.Fatalf("forgetSession waited %s; release should be immediate", time.Since(start))
	}
	if len(page) == 0 || !strings.Contains(string(page[0]), `"error"`) {
		t.Fatalf("expected refusal right after detach: %v", page)
	}
	if _, ok := g.heldExtensionPaints[callKey{"S", 11}]; ok {
		t.Fatal("held paint survived forgetSessionLocked")
	}
}
