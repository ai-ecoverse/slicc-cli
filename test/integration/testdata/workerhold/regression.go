package cdp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func workerHoldGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newGuard(o, nil)
}

func TestWorkerHoldNullOriginDoesNotOverwriteFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/")
	g.noteChildFrameLocked("FRAME", "null")
	if got := g.childFrames["FRAME"]; got != "https://ordinary.test/" {
		t.Fatalf("sandboxed null origin overwrote the frame map: %q", got)
	}
}

func TestWorkerHoldPrunesGoneFrames(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/")
	g.forgetChildFrameLocked("FRAME")
	if _, ok := g.childFrames["FRAME"]; ok {
		t.Fatal("gone frame stayed in the origin map")
	}
}

func TestWorkerHoldDataWorkerFromOrdinarySubFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/embed")
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", parentFrame: "FRAME"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("data: worker from an ordinary sub-frame was not protected")
	}
	if !g.workerFromProtectedCreatorLocked(w) {
		t.Fatal("ordinary sub-frame data: worker was not treated as creator-protected")
	}
}

func TestWorkerHoldDataWorkerFromProtectedSubFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://seven.sliccy.ai/embed")
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", parentFrame: "FRAME"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("data: worker from a protected sub-frame was not protected")
	}
}

func TestWorkerHoldAttachOrdinaryUnshownByID(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["W"] = &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", shown: false}
	if g.targetReasonLocked("W") != "" {
		t.Fatalf("ordinary unshown worker refused by id: %q", g.targetReasonLocked("W"))
	}
	if g.workerConcealedLocked("W") || g.protectedWorkerIDLocked("W") {
		t.Fatal("ordinary worker was concealed")
	}
	send, hide := g.applyResultKindLocked(kindAttach, "W", "", map[string]any{"sessionId": "S-W"})
	if hide {
		t.Fatalf("attach of ordinary unshown worker was hidden: send=%d", len(send))
	}
	if !g.targets["W"].shown {
		t.Fatal("attach by id did not mark the ordinary worker shown")
	}
}

func TestWorkerHoldAttachUnresolvedStillRefused(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["W"] = &target{id: "W", typ: "worker", url: "", parentFrame: "FRAME-unknown", shown: false}
	if reason := g.targetReasonLocked("W"); reason == "" {
		t.Fatal("unresolved worker was attachable by id")
	}
	_, hide := g.applyResultKindLocked(kindAttach, "W", "", map[string]any{"sessionId": "S-W"})
	if !hide {
		t.Fatal("attach of an unresolved worker was admitted")
	}
	if strings.Contains(g.targets["W"].url, "ordinary") {
		t.Fatal("unexpected")
	}
}

func TestWorkerHoldStickyProtectedAfterCreatorNavigatesAway(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://seven.sliccy.ai/embed")
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", parentFrame: "FRAME", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("worker was not protected from its creator")
	}
	_ = g.noteChildFrameLocked("FRAME", "https://ordinary.test/embed")
	if !w.protected {
		t.Fatal("redecide cleared a sticky protected worker after its creator navigated away")
	}
	if g.targetReasonLocked("W") == "" {
		t.Fatal("sticky protected worker became attachable after its creator navigated away")
	}
	if !g.protectedWorkerIDLocked("W") {
		t.Fatal("sticky protected worker became listable after its creator navigated away")
	}
	info := targetInfo{TargetID: "W", Type: "worker", URL: w.url, ParentFrameID: "FRAME"}
	if !g.protectedWorkerLocked(info) {
		t.Fatal("sticky protected worker was not filtered from listings")
	}
}

func TestWorkerHoldUnknownParentFrameStaysHeld(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", opener: "OPENER", parentFrame: "FRAME-unknown", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if w.protected {
		t.Fatal("worker with an ordinary opener was protected before its parentFrame was known")
	}
	if !g.workerUnresolvedLocked(w) {
		t.Fatal("unknown parentFrame did not keep the worker held")
	}
	if reason := g.targetReasonLocked("W"); reason == "" {
		t.Fatal("worker with an unknown parentFrame was attachable by id")
	}
	_, hide := g.applyResultKindLocked(kindAttach, "W", "", map[string]any{"sessionId": "S-W"})
	if !hide {
		t.Fatal("attach of a worker with an unknown parentFrame was admitted")
	}
}

func TestWorkerHoldDataWorkerOrdinaryOpenerStaleFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", opener: "PAGE", openerFrame: "FRAME-stale"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("data: worker with ordinary opener and stale openerFrame was ordinary")
	}
}

func TestWorkerHoldDataWorkerOrdinaryOpenerOpaqueFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.targets["FRAME"] = &target{id: "FRAME", typ: "iframe", url: "://"}
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", opener: "PAGE", openerFrame: "FRAME"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("data: worker with ordinary opener and opaque openerFrame was ordinary")
	}
	if g.targetReasonLocked("W") == "" {
		t.Fatal("opaque-frame data: worker was attachable")
	}
}

func TestWorkerHoldNullOriginOrdinaryWorkerDelivered(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "PAGE"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	g.noteChildFrameLocked("F", "null")
	if !g.creatorClassifiedLocked("F") {
		t.Fatal("null-origin iframe under ordinary page stayed unclassified")
	}
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", parentFrame: "F", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if w.protected || g.workerUnresolvedLocked(w) || g.targetReasonLocked("W") != "" {
		t.Fatalf("ordinary worker from null-origin frame under ordinary: protected=%t unresolved=%t reason=%q", w.protected, g.workerUnresolvedLocked(w), g.targetReasonLocked("W"))
	}
}

func TestWorkerHoldNullOriginUnderSevenStaysHidden(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "SEVEN"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	g.noteChildFrameLocked("F", "null")
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", parentFrame: "F", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected || g.targetReasonLocked("W") == "" {
		t.Fatalf("worker from null-origin under seven was shown: protected=%t reason=%q", w.protected, g.targetReasonLocked("W"))
	}
}

func TestWorkerHoldNullOriginUnknownAncestryStaysHidden(t *testing.T) {
	g := workerHoldGuard(t)
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "UNKNOWN"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	g.noteChildFrameLocked("F", "null")
	if g.creatorClassifiedLocked("F") {
		t.Fatal("null-origin iframe with unknown ancestry was classified")
	}
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", parentFrame: "F", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !g.workerUnresolvedLocked(w) || g.targetReasonLocked("W") == "" {
		t.Fatalf("worker from null-origin with unknown ancestry was shown: unresolved=%t reason=%q", g.workerUnresolvedLocked(w), g.targetReasonLocked("W"))
	}
}

func TestWorkerHoldNullOriginInProcessFrameUnderOrdinary(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.noteFrameParentLocked("FRAME", "PAGE")
	g.noteChildFrameLocked("FRAME", "null")
	if !g.creatorClassifiedLocked("FRAME") {
		t.Fatal("in-process null-origin frame under ordinary stayed unclassified")
	}
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", parentFrame: "FRAME", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if w.protected || g.workerUnresolvedLocked(w) {
		t.Fatalf("worker from in-process null-origin under ordinary stayed held: protected=%t unresolved=%t", w.protected, g.workerUnresolvedLocked(w))
	}
}

func TestWorkerHoldNullOriginAttachUnderCreatorSession(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "PAGE"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	g.sessions["S-f"] = &session{id: "S-f", target: "F", parent: "S-page", typ: "iframe"}
	g.noteFrameParentLocked("F", "PAGE")
	g.attachParent = "S-f"
	defer func() { g.attachParent = "" }()
	if !g.creatorClassifiedLocked("F") {
		t.Fatal("null-origin creator stayed unclassified while its own session attached a worker")
	}
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", parentFrame: "F", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if w.protected || g.workerUnresolvedLocked(w) {
		t.Fatalf("worker attached under null-origin creator session stayed held: protected=%t unresolved=%t", w.protected, g.workerUnresolvedLocked(w))
	}
}

func TestWorkerHoldNullOriginFakeSequence(t *testing.T) {
	g := workerHoldGuard(t)
	allowed := "https://ordinary.test"
	page := targetInfo{TargetID: "P-ordinary", Type: "page", URL: allowed + "/doc"}
	frame := targetInfo{TargetID: "F-null", Type: "iframe", URL: "about:srcdoc", ParentFrameID: "P-ordinary"}
	worker := targetInfo{TargetID: "W-null-ordinary", Type: "worker", URL: allowed + "/worker.js?null-origin-worker=1", ParentFrameID: "F-null"}
	attach := func(parent string, session string, info targetInfo) {
		raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "sessionId": parent, "params": map[string]any{"sessionId": session, "waitingForDebugger": true, "targetInfo": map[string]any{"targetId": info.TargetID, "type": info.Type, "url": info.URL, "parentFrameId": info.ParentFrameID}}})
		if parent == "" {
			raw, _ = json.Marshal(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "waitingForDebugger": true, "targetInfo": map[string]any{"targetId": info.TargetID, "type": info.Type, "url": info.URL, "parentFrameId": info.ParentFrameID}}})
		}
		_ = g.fromBrowser(raw)
	}
	created := func(info targetInfo) {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": info.TargetID, "type": info.Type, "url": info.URL, "parentFrameId": info.ParentFrameID}}})
		_ = g.fromBrowser(raw)
	}
	created(page)
	attach("", "S-page", page)
	created(frame)
	attach("S-page", "S-null", frame)
	raw, _ := json.Marshal(map[string]any{"sessionId": "S-null", "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 1, "origin": "null", "auxData": map[string]any{"isDefault": true, "frameId": "F-null"}}}})
	_ = g.fromBrowser(raw)
	created(worker)
	attach("S-null", "S-null-ordinary", worker)
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.targets["W-null-ordinary"]
	if w == nil || w.protected || g.workerUnresolvedLocked(w) || g.held["S-null-ordinary"] != nil || !w.shown {
		t.Fatalf("fake sequence worker not delivered: protected=%v unresolved=%v held=%v shown=%v classified=%v", w != nil && w.protected, w != nil && g.workerUnresolvedLocked(w), g.held["S-null-ordinary"] != nil, w != nil && w.shown, g.creatorClassifiedLocked("F-null"))
	}
}

func TestWorkerHoldNullOriginChromeInProcessSequence(t *testing.T) {
	g := workerHoldGuard(t)
	allowed := "https://ordinary.test"
	page := targetInfo{TargetID: "PAGE", Type: "page", URL: allowed + "/doc"}
	worker := targetInfo{TargetID: "W", Type: "worker", URL: allowed + "/worker.js?null-origin-worker=1", ParentFrameID: "FRAME", OpenerFrameID: "FRAME"}
	raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": page.TargetID, "type": page.Type, "url": page.URL}}})
	_ = g.fromBrowser(raw)
	raw, _ = json.Marshal(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S-page", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": page.TargetID, "type": page.Type, "url": page.URL}}})
	_ = g.fromBrowser(raw)
	g.mu.Lock()
	g.navSessions["NAV"] = &navTarget{target: "PAGE", session: "NAV", done: true}
	g.mu.Unlock()
	raw, _ = json.Marshal(map[string]any{"sessionId": "NAV", "method": "Page.frameAttached", "params": map[string]any{"frameId": "FRAME", "parentFrameId": "PAGE"}})
	_ = g.fromBrowser(raw)
	raw, _ = json.Marshal(map[string]any{"sessionId": "NAV", "method": "Page.frameNavigated", "params": map[string]any{"frame": map[string]any{"id": "FRAME", "parentId": "PAGE", "url": "about:srcdoc", "securityOrigin": "null"}}})
	_ = g.fromBrowser(raw)
	raw, _ = json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": worker.TargetID, "type": worker.Type, "url": worker.URL, "parentFrameId": worker.ParentFrameID, "openerFrameId": worker.OpenerFrameID}}})
	_ = g.fromBrowser(raw)
	raw, _ = json.Marshal(map[string]any{"method": "Target.attachedToTarget", "sessionId": "S-page", "params": map[string]any{"sessionId": "S-w", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": worker.TargetID, "type": worker.Type, "url": worker.URL, "parentFrameId": worker.ParentFrameID, "openerFrameId": worker.OpenerFrameID}}})
	_ = g.fromBrowser(raw)
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.targets["W"]
	if w == nil || w.protected || g.workerUnresolvedLocked(w) || g.held["S-w"] != nil {
		t.Fatalf("chrome in-process sequence worker not delivered: protected=%v unresolved=%v held=%v classified=%v parents=%q", w != nil && w.protected, w != nil && g.workerUnresolvedLocked(w), g.held["S-w"] != nil, g.creatorClassifiedLocked("FRAME"), g.frameParents["FRAME"])
	}
}

func TestWorkerHoldNullOriginChromeInProcessUnderSeven(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	g.noteFrameParentLocked("FRAME", "PAGE")
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js?null-origin-worker=1", parentFrame: "FRAME", openerFrame: "FRAME", shown: false}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected || g.targetReasonLocked("W") == "" {
		t.Fatalf("in-process null-origin worker under seven was shown: protected=%t reason=%q", w.protected, g.targetReasonLocked("W"))
	}
}
